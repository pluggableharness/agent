package messages

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

const (
	// messagesPath is the streaming completion endpoint.
	messagesPath = "/v1/messages"
	// countTokensPath is the exact-token-count endpoint.
	//
	// #nosec G101 -- a URL path, not a credential. gosec's heuristic
	// flags the substring "token" in a string constant; the actual
	// credential in this package is ClientConfig.APIKey, which is never a
	// literal and never logged.
	countTokensPath = "/v1/messages/count_tokens"
	// anthropicVersion is the vendor API version this adapter speaks. No
	// beta header accompanies it — this package targets only generally
	// available surface.
	anthropicVersion = "2023-06-01"
	// maxErrorResponseBytes caps how much of a non-2xx response body this
	// client reads before classifying it, for the same reason
	// classify.go's maxRawDetailBytes exists.
	maxErrorResponseBytes = 2048
)

// ClientConfig configures a Client.
type ClientConfig struct {
	// BaseURL is the vendor API origin, with no trailing slash.
	BaseURL string
	// APIKey authenticates every request via the x-api-key header.
	APIKey string
	// Timeout bounds each HTTP request's total round-trip time.
	Timeout time.Duration
	// Transport is the RoundTripper to use. Nil selects
	// http.DefaultTransport; tests inject a fake here.
	Transport http.RoundTripper
	// Logger receives this Client's structured logs. Nil selects
	// slog.Default().
	Logger *slog.Logger
}

// Client is a minimal HTTP client for Anthropic's Messages API.
//
// It never retries: classify a failure and return it, always — the
// kernel's internal/modelcall owns retry and backoff
// (.claude/rules/grpc.md's "a provider does not invent its own retry
// policy"; internal/anthropic/CLAUDE.md restates this for the package as
// a whole). A Client method returning a *model.Error with Retryable set is
// the entire extent of this package's opinion on retrying.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	logger  *slog.Logger
}

// NewClient returns a Client configured per cfg.
func NewClient(cfg ClientConfig) *Client {
	transport := cfg.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		http: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
		logger: logger,
	}
}

// setHeaders attaches the headers every Anthropic request carries. The API
// key is never logged or wrapped into an error anywhere in this package —
// see internal/anthropic/CLAUDE.md's secrets section — and this is the
// only place it goes on the wire.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")
}

// cancelOrErr rewrites err to ctx.Err() when ctx has genuinely been
// canceled or exceeded its deadline — cancellation is normal control flow
// (.claude/rules/grpc.md), never wrapped or logged as an application
// error, and returning ctx.Err() itself (rather than a wrap of err) keeps
// errors.Is(err, context.Canceled) working for the caller. When ctx is not
// done, err is returned unchanged.
func cancelOrErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// logRetryable logs a WARN when modelErr classified as retryable — the one
// place this client comments on retryability at all; it never acts on it.
func (c *Client) logRetryable(ctx context.Context, op string, modelErr *model.Error) {
	if modelErr.Retryable {
		c.logger.WarnContext(ctx, "anthropic: "+op+": retryable failure", "category", modelErr.Category)
	}
}

// Stream POSTs req to /v1/messages and drives sink from the resulting SSE
// stream until a terminal event or the stream ends.
func (c *Client) Stream(ctx context.Context, req *Request, sink EventSink) error {
	// Check cancellation before doing any work. net/http does not
	// guarantee it inspects the context before handing the request to the
	// transport, so without this an already-canceled turn could still
	// reach the vendor — a billed request for a turn the kernel has
	// already abandoned. Returned unwrapped so errors.Is(err,
	// context.Canceled) works upstream and pkg/model maps it to a bare
	// codes.Canceled rather than an application error.
	if err := ctx.Err(); err != nil {
		return err
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("anthropic: stream: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+messagesPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("anthropic: stream: build request: %w", err)
	}
	c.setHeaders(httpReq)

	c.logger.DebugContext(ctx, "anthropic: stream: request", "method", httpReq.Method, "path", messagesPath, "model", req.Model)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return cancelOrErr(ctx, fmt.Errorf("anthropic: stream: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	requestID := providerRequestID(resp.Header)
	c.logger.DebugContext(ctx, "anthropic: stream: response", "status", resp.StatusCode, "provider_request_id", requestID)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseBytes))
		modelErr := classifyHTTP(resp.StatusCode, respBody, resp.Header.Get("retry-after"))
		c.logRetryable(ctx, "stream", modelErr)
		return modelErr
	}

	// Emitted before any content so a stream that fails midway is still
	// correlatable to the vendor's own logs — the whole reason stream_start
	// is an early event rather than a field on stop
	// (model/data-types.md#stream_start-and-vendor-request-correlation).
	if requestID != "" {
		if err := sink.StreamStart(requestID); err != nil {
			return err
		}
	}

	return c.drive(ctx, resp.Body, sink)
}

// drive reads body as an SSE stream, translating each decoded event into
// calls on sink until a terminal event is emitted or the stream ends.
func (c *Client) drive(ctx context.Context, body io.Reader, sink EventSink) error {
	translator := NewTranslator(sink)
	scanner := NewScanner(body)

	done := false
	for scanner.Next() {
		var err error
		done, err = translator.Handle(scanner.Event())
		if err != nil {
			return cancelOrErr(ctx, err)
		}
		if done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return cancelOrErr(ctx, err)
	}
	if done {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	// The stream ended cleanly (EOF, no read error) but never produced a
	// terminal event — a silently truncated stream must not look like a
	// clean turn to the kernel.
	truncated := &model.Error{
		Category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN,
		Message:  "anthropic: stream ended without a terminal event",
	}
	return sink.Error(truncated)
}

// providerRequestID extracts the vendor's request identifier from a
// response's headers, or "" when the vendor published none.
//
// Two header names are tried because Anthropic has used both spellings
// across its API surface, and this is best-effort by design: the spec
// permits omitting stream_start entirely, so a header name that stops
// matching costs a correlation id, never a failed request. That is why
// this reads headers rather than parsing the response body — a missing
// header must be a non-event.
func providerRequestID(h http.Header) string {
	for _, name := range []string{"request-id", "x-request-id"} {
		if v := h.Get(name); v != "" {
			return v
		}
	}
	return ""
}

// CountTokens returns an exact input-token count for req against
// POST /v1/messages/count_tokens.
//
// The endpoint takes the same messages/system/tools triple the completion
// endpoint does, so this translates req through the same builders
// BuildRequest uses rather than flattening it to a string — tool schemas
// in particular are frequently the largest single contributor to a
// request's input tokens, and dropping them was the main way the earlier
// text-only shape produced a badly wrong number.
func (c *Client) CountTokens(ctx context.Context, req *modelv1.CountTokensRequest, spec model.Spec) (int64, error) {
	// Same pre-flight cancellation check as Stream, for the same reason.
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	countReq, err := BuildCountTokensRequest(req, spec)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(countReq)
	if err != nil {
		return 0, fmt.Errorf("anthropic: count tokens: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+countTokensPath, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("anthropic: count tokens: build request: %w", err)
	}
	c.setHeaders(httpReq)

	c.logger.DebugContext(ctx, "anthropic: count tokens: request", "method", httpReq.Method, "path", countTokensPath, "model", countReq.Model,
		"messages", len(countReq.Messages), "tools", len(countReq.Tools))

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return 0, cancelOrErr(ctx, fmt.Errorf("anthropic: count tokens: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	c.logger.DebugContext(ctx, "anthropic: count tokens: response", "status", resp.StatusCode)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseBytes))
		modelErr := classifyHTTP(resp.StatusCode, respBody, resp.Header.Get("retry-after"))
		c.logRetryable(ctx, "count tokens", modelErr)
		return 0, modelErr
	}

	var result struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, cancelOrErr(ctx, fmt.Errorf("anthropic: count tokens: decode response: %w", err))
	}
	return result.InputTokens, nil
}
