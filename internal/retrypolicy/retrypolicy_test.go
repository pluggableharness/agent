package retrypolicy

import (
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/config"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		category modelv1.ModelErrorCategory
		want     Reaction
	}{
		{
			name:     "unspecified -> fail",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNSPECIFIED,
			want:     ReactionFail,
		},
		{
			name:     "rate_limited -> retry",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_RATE_LIMITED,
			want:     ReactionRetry,
		},
		{
			name:     "overloaded -> retry",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_OVERLOADED,
			want:     ReactionRetry,
		},
		{
			name:     "context_length_exceeded -> reduce_context",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTEXT_LENGTH_EXCEEDED,
			want:     ReactionReduceContext,
		},
		{
			name:     "auth_error -> fail",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_AUTH_ERROR,
			want:     ReactionFail,
		},
		{
			name:     "invalid_request -> fail",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
			want:     ReactionFail,
		},
		{
			name:     "content_filtered -> surface",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_CONTENT_FILTERED,
			want:     ReactionSurface,
		},
		{
			name:     "unknown -> fail",
			category: modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN,
			want:     ReactionFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.category)
			if got != tt.want {
				t.Errorf("Classify(%v) = %v, want %v", tt.category, got, tt.want)
			}
		})
	}
}

func TestFromConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		cfg        config.RetrySettings
		sessionMax int
		want       Settings
	}{
		{
			name:       "canonical defaults",
			cfg:        config.DefaultRetrySettings,
			sessionMax: 10,
			want: Settings{
				BaseDelay:         500 * time.Millisecond,
				BackoffFactor:     2,
				MaxRetries:        5,
				SessionMaxRetries: 10,
			},
		},
		{
			name: "custom values",
			cfg: config.RetrySettings{
				BaseDelayMS:   100,
				BackoffFactor: 3,
				MaxRetries:    8,
			},
			sessionMax: 20,
			want: Settings{
				BaseDelay:         100 * time.Millisecond,
				BackoffFactor:     3,
				MaxRetries:        8,
				SessionMaxRetries: 20,
			},
		},
		{
			name:       "zero session max",
			cfg:        config.DefaultRetrySettings,
			sessionMax: 0,
			want: Settings{
				BaseDelay:         500 * time.Millisecond,
				BackoffFactor:     2,
				MaxRetries:        5,
				SessionMaxRetries: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromConfig(tt.cfg, tt.sessionMax)
			if got != tt.want {
				t.Errorf("FromConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDelay_WithRetryAfter(t *testing.T) {
	t.Parallel()
	s := Settings{
		BaseDelay:     500 * time.Millisecond,
		BackoffFactor: 2,
	}

	tests := []struct {
		name       string
		attempt    int
		retryAfter *time.Duration
		jitter     float64
		want       time.Duration
	}{
		{
			name:       "retryAfter overrides attempt 1 jitter 0",
			attempt:    1,
			retryAfter: ptrDuration(2 * time.Second),
			jitter:     0.0,
			want:       2 * time.Second,
		},
		{
			name:       "retryAfter overrides attempt 5 jitter 0.999",
			attempt:    5,
			retryAfter: ptrDuration(100 * time.Millisecond),
			jitter:     0.999,
			want:       100 * time.Millisecond,
		},
		{
			name:       "retryAfter ignored when nil",
			attempt:    1,
			retryAfter: nil,
			jitter:     0.0,
			want:       250 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Delay(s, tt.attempt, tt.retryAfter, tt.jitter)
			if got != tt.want {
				t.Errorf("Delay() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDelay_Backoff(t *testing.T) {
	t.Parallel()
	s := Settings{
		BaseDelay:     500 * time.Millisecond,
		BackoffFactor: 2,
	}

	testCases := []struct {
		name           string
		jitter         float64
		expectedDelays []time.Duration
	}{
		{
			name:   "jitter=0.0",
			jitter: 0.0,
			expectedDelays: []time.Duration{
				250 * time.Millisecond, // 500 * 2^0 * 0.5 = 250
				500 * time.Millisecond, // 500 * 2^1 * 0.5 = 500
				1 * time.Second,        // 500 * 2^2 * 0.5 = 1000
				2 * time.Second,        // 500 * 2^3 * 0.5 = 2000
				4 * time.Second,        // 500 * 2^4 * 0.5 = 4000
			},
		},
		{
			name:   "jitter=0.999",
			jitter: 0.999,
			expectedDelays: []time.Duration{
				time.Duration(float64(500*time.Millisecond) * 1 * (0.5 + 0.5*0.999)),  // 2^0 = 1
				time.Duration(float64(500*time.Millisecond) * 2 * (0.5 + 0.5*0.999)),  // 2^1 = 2
				time.Duration(float64(500*time.Millisecond) * 4 * (0.5 + 0.5*0.999)),  // 2^2 = 4
				time.Duration(float64(500*time.Millisecond) * 8 * (0.5 + 0.5*0.999)),  // 2^3 = 8
				time.Duration(float64(500*time.Millisecond) * 16 * (0.5 + 0.5*0.999)), // 2^4 = 16
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for i, expectedDelay := range tc.expectedDelays {
				attempt := i + 1 // 1-indexed
				got := Delay(s, attempt, nil, tc.jitter)
				if got != expectedDelay {
					t.Errorf("Delay(attempt=%d, jitter=%v) = %v, want %v",
						attempt, tc.jitter, got, expectedDelay)
				}
			}
		})
	}
}

func TestDelay_MonotonicIncreasing(t *testing.T) {
	t.Parallel()
	s := Settings{
		BaseDelay:     500 * time.Millisecond,
		BackoffFactor: 2,
	}

	jitterValues := []float64{0.0, 0.5, 0.999}
	for _, jitter := range jitterValues {
		t.Run("jitter="+formatFloat(jitter), func(t *testing.T) {
			var prevDelay time.Duration
			for attempt := 1; attempt <= 5; attempt++ {
				delay := Delay(s, attempt, nil, jitter)
				if attempt > 1 && delay < prevDelay {
					t.Errorf("Delay(attempt=%d, jitter=%v) = %v < %v (previous), not monotonically non-decreasing",
						attempt, jitter, delay, prevDelay)
				}
				prevDelay = delay
			}
		})
	}
}

func TestDelay_NegativeAttempt(t *testing.T) {
	t.Parallel()
	s := Settings{
		BaseDelay:     500 * time.Millisecond,
		BackoffFactor: 2,
	}

	// Negative attempt should be treated as 1
	got := Delay(s, -1, nil, 0.0)
	want := 250 * time.Millisecond // Same as attempt=1
	if got != want {
		t.Errorf("Delay(attempt=-1) = %v, want %v", got, want)
	}
}

func TestDelay_ZeroBackoffFactor(t *testing.T) {
	t.Parallel()
	s := Settings{
		BaseDelay:     500 * time.Millisecond,
		BackoffFactor: 0,
	}

	// With backoff factor 0, 0^0 = 1, so delay = 500 * 1 * 0.5 = 250ms
	got := Delay(s, 1, nil, 0.0)
	want := 250 * time.Millisecond
	if got != want {
		t.Errorf("Delay with backoffFactor=0 = %v, want %v", got, want)
	}
}

func TestDelay_LargeBackoffFactor(t *testing.T) {
	t.Parallel()
	s := Settings{
		BaseDelay:     1 * time.Millisecond,
		BackoffFactor: 10,
	}

	// With large backoff factor, later attempts should have much larger delays
	delays := make([]time.Duration, 5)
	for i := 0; i < 5; i++ {
		delays[i] = Delay(s, i+1, nil, 0.0)
	}

	// Check exponential growth: each should be roughly 10x the previous
	for i := 1; i < 5; i++ {
		if delays[i] <= delays[i-1]*5 {
			t.Errorf("Delay growth insufficient: delays[%d]=%v, delays[%d]=%v",
				i, delays[i], i-1, delays[i-1])
		}
	}
}

func TestDelay_CustomBaseDelay(t *testing.T) {
	t.Parallel()
	s := Settings{
		BaseDelay:     100 * time.Millisecond,
		BackoffFactor: 2,
	}

	got := Delay(s, 1, nil, 0.0)
	want := 50 * time.Millisecond // 100 * 2^0 * 0.5
	if got != want {
		t.Errorf("Delay(custom baseDelay) = %v, want %v", got, want)
	}
}

func TestClassify_DefaultReactionIsFail(t *testing.T) {
	t.Parallel()
	// Verify that the default conservative behavior is ReactionFail
	// for any unhandled or future enum values
	var unknownCategory modelv1.ModelErrorCategory = 999
	got := Classify(unknownCategory)
	if got != ReactionFail {
		t.Errorf("Classify(unknown) = %v, want ReactionFail", got)
	}
}

// Helper functions

func ptrDuration(d time.Duration) *time.Duration {
	return &d
}

func formatFloat(f float64) string {
	if f == 0.0 {
		return "0"
	}
	return "0.999"
}
