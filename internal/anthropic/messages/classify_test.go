package messages

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func apiErrorBody(t *testing.T, errType, message string) []byte {
	t.Helper()
	body, err := json.Marshal(APIError{
		Type:      "error",
		Error:     APIErrorBody{Type: errType, Message: message},
		RequestID: "req_123",
	})
	if err != nil {
		t.Fatalf("marshal fixture APIError: %v", err)
	}
	return body
}

func TestClassifyHTTP_table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		errType   string
		wantCat   modelv1.ModelErrorCategory
		wantRetry bool
	}{
		{"invalid_request_error/400", 400, errInvalidRequest, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
		{"authentication_error/401", 401, errAuthentication, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
		{"billing_error/402", 402, errBilling, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
		{"permission_error/403", 403, errPermission, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
		{"not_found_error/404", 404, errNotFound, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
		{"conflict_error/409", 409, errConflict, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
		{"request_too_large/413", 413, errRequestTooLarge, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED, false},
		{"rate_limit_error/429", 429, errRateLimit, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, true},
		{"api_error/500", 500, errAPI, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		{"timeout_error/504", 504, errTimeout, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		{"overloaded_error/529", 529, errOverloaded, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		{"unrecognized type/418", 418, "teapot_error", modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := apiErrorBody(t, tt.errType, "something went wrong")
			got := classifyHTTP(tt.status, body, "")
			if got.Category != tt.wantCat {
				t.Errorf("Category = %v, want %v", got.Category, tt.wantCat)
			}
			if got.Retryable != tt.wantRetry {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tt.wantRetry)
			}
			if !strings.HasPrefix(got.Message, "anthropic: ") {
				t.Errorf("Message = %q, missing anthropic: prefix", got.Message)
			}
			if !strings.Contains(got.RawDetail, tt.errType) || !strings.Contains(got.RawDetail, "req_123") {
				t.Errorf("RawDetail = %q, missing type/request_id", got.RawDetail)
			}
		})
	}
}

func TestClassifyHTTP_contextLengthSniff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		want    bool
	}{
		{"prompt is too long", "prompt is too long: 250000 tokens > 200000 maximum", true},
		{"too many tokens", "too many tokens in the request", true},
		{"exceeds the maximum", "input exceeds the maximum context length", true},
		{"unrelated message", "field 'model' is required", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := apiErrorBody(t, errInvalidRequest, tt.message)
			got := classifyHTTP(400, body, "")
			wantCat := modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST
			if tt.want {
				wantCat = modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED
			}
			if got.Category != wantCat {
				t.Errorf("Category = %v, want %v", got.Category, wantCat)
			}
			if got.Retryable {
				t.Errorf("Retryable = true, want false")
			}
		})
	}
}

func TestClassifyHTTP_retryAfterParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryAfter string
		wantSet    bool
		wantDur    time.Duration
	}{
		{"valid seconds", "5", true, 5 * time.Second},
		{"zero", "0", true, 0},
		{"empty", "", false, 0},
		{"malformed non-numeric", "not-a-number", false, 0},
		{"http-date is not seconds and is ignored", "Wed, 21 Oct 2026 07:28:00 GMT", false, 0},
		{"negative is ignored", "-1", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := apiErrorBody(t, errRateLimit, "slow down")
			got := classifyHTTP(429, body, tt.retryAfter)
			if tt.wantSet && got.RetryAfter != tt.wantDur {
				t.Errorf("RetryAfter = %v, want %v", got.RetryAfter, tt.wantDur)
			}
			if !tt.wantSet && got.RetryAfter != 0 {
				t.Errorf("RetryAfter = %v, want unset (0)", got.RetryAfter)
			}
		})
	}
}

func TestClassifyHTTP_retryAfterOnlySetWhenRetryable(t *testing.T) {
	t.Parallel()

	// invalid_request_error is not retryable; a stray retry-after header
	// (e.g. from an intermediary proxy) must not be honored.
	body := apiErrorBody(t, errInvalidRequest, "bad request")
	got := classifyHTTP(400, body, "5")
	if got.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 for a non-retryable category", got.RetryAfter)
	}
}

func TestClassifyHTTP_unparseableBodyFallsBackToStatus(t *testing.T) {
	t.Parallel()

	htmlBody := []byte("<html><body>502 Bad Gateway</body></html>")

	tests := []struct {
		name      string
		status    int
		wantCat   modelv1.ModelErrorCategory
		wantRetry bool
	}{
		{"5xx with html body falls back to overloaded/retryable", 502, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		{"exact 500 table entry still applies", 500, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		{"non-5xx unparseable body is unknown", 404, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
		{"totally unmapped status", 418, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyHTTP(tt.status, htmlBody, "")
			if got.Category != tt.wantCat {
				t.Errorf("Category = %v, want %v", got.Category, tt.wantCat)
			}
			if got.Retryable != tt.wantRetry {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tt.wantRetry)
			}
		})
	}
}

func TestClassifyHTTP_rawDetailCapped(t *testing.T) {
	t.Parallel()

	hugeBody := []byte(strings.Repeat("x", maxRawDetailBytes*4))
	got := classifyHTTP(500, hugeBody, "")
	if len(got.RawDetail) > maxRawDetailBytes+128 {
		// +128 for the "type=... status=... body=" prefix this function
		// prepends before the capped body bytes.
		t.Errorf("RawDetail length = %d, want roughly capped at %d", len(got.RawDetail), maxRawDetailBytes)
	}
}

func TestClassifyHTTP_emptyMessageUsesStatus(t *testing.T) {
	t.Parallel()

	got := classifyHTTP(503, []byte(""), "")
	if !strings.Contains(got.Message, strconv.Itoa(503)) {
		t.Errorf("Message = %q, want it to mention the status", got.Message)
	}
}

func TestClassifyStreamError_table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		errType   string
		wantCat   modelv1.ModelErrorCategory
		wantRetry bool
	}{
		{"rate_limit_error", errRateLimit, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, true},
		{"overloaded_error", errOverloaded, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		{"invalid_request_error", errInvalidRequest, modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
		{"unrecognized", "mystery_error", modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyStreamError(APIErrorBody{Type: tt.errType, Message: "vendor message"})
			if got.Category != tt.wantCat {
				t.Errorf("Category = %v, want %v", got.Category, tt.wantCat)
			}
			if got.Retryable != tt.wantRetry {
				t.Errorf("Retryable = %v, want %v", got.Retryable, tt.wantRetry)
			}
			if !strings.HasPrefix(got.Message, "anthropic: ") {
				t.Errorf("Message = %q, missing anthropic: prefix", got.Message)
			}
		})
	}
}

func TestClassifyStreamError_contextLengthSniff(t *testing.T) {
	t.Parallel()

	got := classifyStreamError(APIErrorBody{
		Type:    errInvalidRequest,
		Message: "prompt is too long: 300000 tokens > 200000 maximum",
	})
	if got.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED {
		t.Errorf("Category = %v, want CONTEXT_LENGTH_EXCEEDED", got.Category)
	}
	if got.Retryable {
		t.Errorf("Retryable = true, want false")
	}
}

func TestClassifyStreamError_emptyMessage(t *testing.T) {
	t.Parallel()

	got := classifyStreamError(APIErrorBody{})
	if got.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN {
		t.Errorf("Category = %v, want UNKNOWN", got.Category)
	}
	if !strings.Contains(got.Message, "no error message provided") {
		t.Errorf("Message = %q, want a fallback message", got.Message)
	}
}
