package retrypolicy

import (
	"math"
	"time"

	"github.com/pluggableharness/agent/internal/config"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Reaction is the kernel's classified response to a model-provider error
// category, per error-recovery.md#model-provider-errors.
type Reaction int

const (
	// ReactionFail indicates the error should not be retried.
	ReactionFail Reaction = iota
	// ReactionRetry indicates the error should be retried with backoff.
	ReactionRetry
	// ReactionReduceContext indicates context reduction is needed.
	ReactionReduceContext
	// ReactionSurface indicates the error should be surfaced distinctly.
	ReactionSurface
)

// Settings is the kernel's retry/backoff configuration.
type Settings struct {
	BaseDelay         time.Duration
	BackoffFactor     int
	MaxRetries        int // per-attempt-chain cap
	SessionMaxRetries int // separate, session-wide cap
}

// FromConfig bridges internal/config.RetrySettings (already-decoded
// agent.hcl settings.retry{} block) into this package's Settings,
// applying sessionMax as the separate session-wide cap
// error-recovery.md requires be tracked independently of the per-attempt
// cap.
func FromConfig(s config.RetrySettings, sessionMax int) Settings {
	return Settings{
		BaseDelay:         time.Duration(s.BaseDelayMS) * time.Millisecond,
		BackoffFactor:     s.BackoffFactor,
		MaxRetries:        s.MaxRetries,
		SessionMaxRetries: sessionMax,
	}
}

// Classify maps a model-provider error category to this package's
// Reaction, per error-recovery.md#model-provider-errors' four-way split.
// An unrecognized/unspecified category classifies as ReactionFail (the
// conservative default — never silently retried).
func Classify(c modelv1.ModelErrorCategory) Reaction {
	switch c {
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED:
		return ReactionRetry
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED:
		return ReactionRetry
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED:
		return ReactionReduceContext
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTENT_FILTERED:
		return ReactionSurface
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR:
		return ReactionFail
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST:
		return ReactionFail
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN:
		return ReactionFail
	case modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNSPECIFIED:
		return ReactionFail
	default:
		return ReactionFail
	}
}

// Delay computes the backoff before attempt's retry (attempt is
// 1-indexed: the first retry is attempt=1). When retryAfter is non-nil,
// it is honored verbatim (error-recovery.md MUST), overriding the
// computed backoff entirely. Otherwise:
//
//	delay = s.BaseDelay * s.BackoffFactor^(attempt-1) * (0.5 + 0.5*jitter)
//
// jitter is caller-supplied in [0, 1) so this stays a pure function —
// production code supplies math/rand-derived jitter; tests pin it to
// fixed values (0.0 and 0.999) for deterministic assertions.
func Delay(s Settings, attempt int, retryAfter *time.Duration, jitter float64) time.Duration {
	if retryAfter != nil {
		return *retryAfter
	}

	if attempt < 1 {
		attempt = 1
	}

	// Compute backoff: baseDelay * (backoffFactor ^ (attempt - 1))
	exponent := float64(attempt - 1)
	factor := math.Pow(float64(s.BackoffFactor), exponent)
	backoff := float64(s.BaseDelay) * factor

	// Apply jitter: multiply by (0.5 + 0.5*jitter)
	jitterMultiplier := 0.5 + 0.5*jitter
	delay := time.Duration(backoff * jitterMultiplier)

	return delay
}
