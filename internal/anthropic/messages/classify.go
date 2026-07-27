package messages

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// maxRawDetailBytes caps how much of a raw error body classify copies into
// model.Error.RawDetail — a misbehaving proxy can return a multi-megabyte
// HTML error page, and RawDetail exists for debugging, not for holding the
// whole thing.
const maxRawDetailBytes = 2048

// contextLengthPhrases are the message substrings classifyHTTP and
// classifyStreamError use to detect an over-long-prompt failure. See the
// comment where this is applied for why the technique itself is fragile.
var contextLengthPhrases = []string{
	"prompt is too long",
	"too many tokens",
	"exceeds the maximum",
}

// errorClassification is one row of the vendor-error-type → model error
// mapping tables below.
type errorClassification struct {
	category  modelv1.ModelErrorCategory
	retryable bool
}

// errorTypeTable maps Anthropic's error.type values to a category and
// retryability, per docs/specifications/model/conformance.md's taxonomy.
// This is the primary classification path — keyed off the parsed error
// body, which is present on both an HTTP error response and a mid-stream
// error event.
var errorTypeTable = map[string]errorClassification{
	errInvalidRequest:  {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
	errAuthentication:  {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
	errBilling:         {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
	errPermission:      {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
	errNotFound:        {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
	errConflict:        {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
	errRequestTooLarge: {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED, false},
	errRateLimit:       {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, true},
	errAPI:             {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
	errTimeout:         {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
	errOverloaded:      {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
}

// classify resolves a category and retryability from errType first,
// falling back to the HTTP status when errType is empty or unrecognized
// (an unparseable body — an HTML proxy error page carries no error.type at
// all).
//
// The status fallback is model.ClassifyHTTPStatus, shared with every other
// provider: those are HTTP semantics rather than Anthropic's, and its 5xx
// rule is what makes Anthropic's own 529 overload code classify correctly
// without an entry anywhere. Only errorTypeTable above is vendor-specific.
func classify(errType string, status int) (modelv1.ModelErrorCategory, bool) {
	if c, ok := errorTypeTable[errType]; ok {
		return c.category, c.retryable
	}
	return model.ClassifyHTTPStatus(status)
}

// looksLikeContextLength reports whether message reads as Anthropic's
// over-long-prompt wording. Anthropic has no distinct error.type for this
// case — it's a plain 400 invalid_request_error whose message happens to
// say the prompt is too long — so detection is a small, case-insensitive
// substring check.
func looksLikeContextLength(message string) bool {
	lower := strings.ToLower(message)
	for _, phrase := range contextLengthPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// upgradeContextLength promotes category to CONTEXT_LENGTH_EXCEEDED when
// it classified as INVALID_REQUEST and message looks like an over-long
// prompt.
//
// This is message-sniffing and therefore fragile: it depends entirely on
// Anthropic's current wording. If the vendor rewords the message, this
// silently stops matching and classification degrades back to
// invalid_request — a safe failure direction (the kernel still won't
// retry it as-is), but one a future reader should know about rather than
// discover by surprise. That safety property — degrading to a category
// the kernel already treats correctly, never to something worse — is the
// justification for doing message-sniffing here at all.
func upgradeContextLength(category modelv1.ModelErrorCategory, message string) modelv1.ModelErrorCategory {
	if category == modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST && looksLikeContextLength(message) {
		return modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED
	}
	return category
}

// classifyHTTP maps an Anthropic HTTP error response to a *model.Error.
// retryAfter is the raw retry-after header value, if any; body is the
// (possibly capped) response body.
func classifyHTTP(status int, body []byte, retryAfter string) *model.Error {
	var apiErr APIError
	_ = json.Unmarshal(body, &apiErr) // unparseable body (e.g. an HTML proxy error page) leaves apiErr zero-valued, handled by classify's status fallback.

	category, retryable := classify(apiErr.Error.Type, status)
	category = upgradeContextLength(category, apiErr.Error.Message)

	message := apiErr.Error.Message
	if message == "" {
		message = fmt.Sprintf("http status %d", status)
	}

	modelErr := &model.Error{
		Category:  category,
		Message:   "anthropic: " + message,
		Retryable: retryable,
		RawDetail: rawDetail(apiErr.Error.Type, status, apiErr.RequestID, body),
	}
	if retryable {
		if d, ok := parseRetryAfterSeconds(retryAfter); ok {
			modelErr.RetryAfter = d
		}
	}
	return modelErr
}

// classifyStreamError maps a mid-stream SSE error event to a *model.Error.
// There is no HTTP status or retry-after header available mid-stream, so
// classification rests entirely on the vendor's error.type.
func classifyStreamError(body APIErrorBody) *model.Error {
	category, retryable := classify(body.Type, 0)
	category = upgradeContextLength(category, body.Message)

	message := body.Message
	if message == "" {
		message = "no error message provided"
	}

	return &model.Error{
		Category:  category,
		Message:   "anthropic: " + message,
		Retryable: retryable,
		RawDetail: fmt.Sprintf("type=%s", body.Type),
	}
}

// rawDetail assembles model.Error.RawDetail from the pieces available on
// an HTTP error response, capping the body so a huge proxy error page
// cannot balloon a log line.
func rawDetail(errType string, status int, requestID string, body []byte) string {
	capped := body
	if len(capped) > maxRawDetailBytes {
		capped = capped[:maxRawDetailBytes]
	}
	detail := fmt.Sprintf("type=%s status=%d", errType, status)
	if requestID != "" {
		detail += " request_id=" + requestID
	}
	detail += " body=" + string(capped)
	return detail
}

// parseRetryAfterSeconds parses Anthropic's retry-after header value,
// which is always an integer count of seconds — never an HTTP-date, unlike
// some other vendors' retry-after headers. A malformed value is ignored
// rather than failing classification outright.
func parseRetryAfterSeconds(raw string) (time.Duration, bool) {
	if raw == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
