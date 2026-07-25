// Package retrypolicy implements model-provider error-category classification
// and backoff/jitter delay computation.
//
// It classifies errors returned by model providers into reaction categories
// (fail, retry, reduce-context, or surface) per
// docs/specifications/agent-loop/error-recovery.md#model-provider-errors.
// The kernel uses this classification to decide whether to retry a failed
// model call, and if retrying, computes the backoff delay before the next
// attempt using exponential backoff with jitter, honoring any explicit
// retry-after directive from the provider.
package retrypolicy
