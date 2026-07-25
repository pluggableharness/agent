package cost

import (
	"math"
	"testing"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

func ptrF64(v float64) *float64 { return &v }
func ptrI64(v int64) *int64     { return &v }

func approxEqual(a, b float64) bool {
	const epsilon = 1e-9
	return math.Abs(a-b) < epsilon
}

// TestComputeWorkedExample is the hand-computed worked example from the
// task brief: input 1000 @ $3/Mtok, output 500 @ $15/Mtok, cache-write
// 200 @ $3.75/Mtok, cache-read 800 @ $0.30/Mtok, reasoning 100 tokens
// billed at the $15/Mtok output rate (its own term, not folded into
// output_tokens).
//
//	0.003   (input:     1000 * 3.00  / 1e6)
//	0.0075  (output:     500 * 15.00 / 1e6)
//	0.00075 (cache write: 200 * 3.75  / 1e6)
//	0.00024 (cache read:  800 * 0.30  / 1e6)
//	0.0015  (reasoning:  100 * 15.00 / 1e6, at the output rate)
//	-------
//	0.01299
func TestComputeWorkedExample(t *testing.T) {
	t.Parallel()

	tier := &modelv1.PricingTier{
		InputPerMtok:      3.00,
		OutputPerMtok:     15.00,
		CacheWritePerMtok: ptrF64(3.75),
		CacheReadPerMtok:  ptrF64(0.30),
	}
	usage := &modelv1.Usage{
		InputTokens:      1000,
		OutputTokens:     500,
		CacheWriteTokens: ptrI64(200),
		CacheReadTokens:  ptrI64(800),
		ReasoningTokens:  ptrI64(100),
	}

	const want = 0.01299
	got := Compute(tier, usage)
	if !approxEqual(got, want) {
		t.Fatalf("Compute() = %v, want %v", got, want)
	}
}

func TestCompute(t *testing.T) {
	t.Parallel()

	tier := &modelv1.PricingTier{
		InputPerMtok:      2.0,
		OutputPerMtok:     10.0,
		CacheWritePerMtok: ptrF64(1.5),
		CacheReadPerMtok:  ptrF64(0.5),
	}

	tests := []struct {
		name  string
		tier  *modelv1.PricingTier
		usage *modelv1.Usage
		want  float64
	}{
		{
			name:  "input only",
			tier:  tier,
			usage: &modelv1.Usage{InputTokens: 1_000_000},
			want:  2.0,
		},
		{
			name:  "output only",
			tier:  tier,
			usage: &modelv1.Usage{OutputTokens: 1_000_000},
			want:  10.0,
		},
		{
			name:  "cache write only",
			tier:  tier,
			usage: &modelv1.Usage{CacheWriteTokens: ptrI64(1_000_000)},
			want:  1.5,
		},
		{
			name:  "cache read only",
			tier:  tier,
			usage: &modelv1.Usage{CacheReadTokens: ptrI64(1_000_000)},
			want:  0.5,
		},
		{
			name:  "reasoning billed at output rate, as its own term",
			tier:  tier,
			usage: &modelv1.Usage{ReasoningTokens: ptrI64(1_000_000)},
			want:  10.0,
		},
		{
			name: "reasoning is never folded into output_tokens — both present sum independently",
			tier: tier,
			usage: &modelv1.Usage{
				OutputTokens:    1_000_000,
				ReasoningTokens: ptrI64(1_000_000),
			},
			want: 20.0,
		},
		{
			name:  "nil optional usage fields treated as zero",
			tier:  tier,
			usage: &modelv1.Usage{InputTokens: 500_000},
			want:  1.0,
		},
		{
			name:  "zero usage yields zero cost",
			tier:  tier,
			usage: &modelv1.Usage{},
			want:  0,
		},
		{
			name:  "nil usage treated as all-zero",
			tier:  tier,
			usage: nil,
			want:  0,
		},
		{
			name:  "nil tier treated as all-zero rates",
			tier:  nil,
			usage: &modelv1.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
			want:  0,
		},
		{
			name: "all five terms combined",
			tier: tier,
			usage: &modelv1.Usage{
				InputTokens:      1_000_000,
				OutputTokens:     1_000_000,
				CacheWriteTokens: ptrI64(1_000_000),
				CacheReadTokens:  ptrI64(1_000_000),
				ReasoningTokens:  ptrI64(1_000_000),
			},
			want: 2.0 + 10.0 + 1.5 + 0.5 + 10.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Compute(tt.tier, tt.usage)
			if !approxEqual(got, tt.want) {
				t.Errorf("Compute() = %v, want %v", got, tt.want)
			}
		})
	}
}
