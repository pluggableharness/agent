package kernelcallback

import commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"

// categoryTextTable maps a plugin Category to the lowercase text used when
// building a server-derived, dot-separated name (an event-bus topic via
// Publish, or a RecordMetrics instrument name) — the same lowercase
// vocabulary internal/statebackend's own producerCategoryText uses for its
// stored column values, kept as an independent copy here rather than an
// import: statebackend's map is that package's own storage-encoding detail
// (its own comment notes state-backend.md leaves the column's exact text
// undocumented), while this package's use is a wire-facing protocol detail
// event-bus.md documents generically as "category" — the two happen to
// agree today but are conceptually owned by different specs.
// CATEGORY_UNSPECIFIED is deliberately absent — a producer's category MUST
// NOT ever be unspecified (kernel-callbacks.md's server-derived producer
// identity is always a real category).
var categoryTextTable = map[commonv1.Category]string{
	commonv1.Category_CATEGORY_MODEL:        "model",
	commonv1.Category_CATEGORY_TOOL:         "tool",
	commonv1.Category_CATEGORY_CONTEXT:      "context",
	commonv1.Category_CATEGORY_MEMORY:       "memory",
	commonv1.Category_CATEGORY_FRONTEND:     "frontend",
	commonv1.Category_CATEGORY_WIDGET:       "widget",
	commonv1.Category_CATEGORY_SLASHCOMMAND: "slashcommand",
}

// categoryText renders category as its lowercase text form. An
// unrecognized or unspecified category (which should never occur — see
// categoryTextTable's comment) renders as "unspecified" rather than
// panicking or producing an empty path segment.
func categoryText(category commonv1.Category) string {
	if text, ok := categoryTextTable[category]; ok {
		return text
	}
	return "unspecified"
}
