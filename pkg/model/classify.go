package model

import (
	"net/http"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// httpStatusCategories maps an HTTP status to the error category
// docs/specifications/model/conformance.md's taxonomy assigns it, and
// whether the kernel may retry it.
//
// These are HTTP semantics, not any one vendor's: a 401 means the same
// thing everywhere. Only statuses whose meaning is unambiguous appear
// here; anything else falls through to ClassifyHTTPStatus's 5xx rule or
// to UNKNOWN.
var httpStatusCategories = map[int]struct {
	category  modelv1.ModelErrorCategory
	retryable bool
}{
	http.StatusBadRequest:            {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
	http.StatusUnauthorized:          {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
	http.StatusPaymentRequired:       {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
	http.StatusForbidden:             {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR, false},
	http.StatusNotFound:              {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
	http.StatusConflict:              {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
	http.StatusRequestEntityTooLarge: {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED, false},
	http.StatusUnprocessableEntity:   {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST, false},
	http.StatusTooManyRequests:       {modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED, true},
}

// ClassifyHTTPStatus maps an HTTP status to its model error category and
// whether the kernel may retry it, per
// docs/specifications/model/conformance.md#error-taxonomy.
//
// It exists so every provider does not re-derive the same table, and so
// the two mistakes that actually hurt are made once rather than per
// vendor: treating a 403 as retryable (it is a policy refusal, and
// retrying it burns quota against a request that can never succeed), and
// treating a 413 as a generic invalid request (it is a context overflow,
// and only the CONTEXT_LENGTH_EXCEEDED category tells the kernel to shrink
// the conversation and try again).
//
// Any 5xx without an entry of its own reads as OVERLOADED and retryable:
// a 5xx-equivalent response is by definition the vendor's own transient
// failure, whatever number it carries. This is what makes a vendor-
// specific overload code — Anthropic's 529, say — classify correctly with
// no table entry.
//
// A provider whose vendor publishes a structured error vocabulary SHOULD
// consult that first and use this only as the fallback for a body that is
// missing or unparseable, which is exactly the case an HTML error page
// from an intermediate proxy produces.
func ClassifyHTTPStatus(status int) (category modelv1.ModelErrorCategory, retryable bool) {
	if c, ok := httpStatusCategories[status]; ok {
		return c.category, c.retryable
	}
	if status >= http.StatusInternalServerError {
		return modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED, true
	}
	return modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN, false
}
