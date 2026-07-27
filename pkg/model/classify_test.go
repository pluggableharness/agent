package model_test

import (
	"net/http"
	"testing"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func TestClassifyHTTPStatus(t *testing.T) {
	t.Parallel()

	tests := map[int]struct {
		category  modelv1.ModelErrorCategory
		retryable bool
	}{
		http.StatusBadRequest:      {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
		http.StatusUnauthorized:    {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
		http.StatusTooManyRequests: {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, true},
		// 413 must not degrade to a generic invalid request: only the
		// context-length category tells the kernel to shrink and retry.
		http.StatusRequestEntityTooLarge: {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED, false},
		// A 5xx with no entry of its own still reads as a transient vendor
		// failure — this is what makes a vendor-specific overload code
		// classify correctly with no table entry.
		http.StatusBadGateway:         {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		http.StatusServiceUnavailable: {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		529:                           {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true},
		// An unmapped 4xx is genuinely unclassifiable, and unknown is
		// non-retryable by default per the taxonomy.
		http.StatusTeapot: {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN, false},
	}

	for status, want := range tests {
		gotCategory, gotRetryable := model.ClassifyHTTPStatus(status)
		if gotCategory != want.category || gotRetryable != want.retryable {
			t.Errorf("ClassifyHTTPStatus(%d) = (%v, %v), want (%v, %v)",
				status, gotCategory, gotRetryable, want.category, want.retryable)
		}
	}
}

func TestClassifyHTTPStatus_forbiddenIsNeverRetryable(t *testing.T) {
	t.Parallel()

	// Called out on its own because it is the mistake that costs money: a
	// 403 is a policy refusal, so retrying it burns quota against a request
	// that can never succeed.
	category, retryable := model.ClassifyHTTPStatus(http.StatusForbidden)
	if retryable {
		t.Error("ClassifyHTTPStatus(403) is retryable, want false")
	}
	if category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR {
		t.Errorf("ClassifyHTTPStatus(403) category = %v, want AUTH_ERROR", category)
	}
}
