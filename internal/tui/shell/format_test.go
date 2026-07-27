package shell

import (
	"strings"
	"testing"
	"time"
)

func TestFormatTokens(t *testing.T) {
	t.Parallel()

	tests := map[int64]string{
		0: "0", 42: "42", 999: "999",
		1_000: "1k", 1_240: "1.2k", 18_200: "18.2k", 999_000: "999k",
		1_000_000: "1M", 2_450_000: "2.5M",
		-5: "0",
	}

	for in, want := range tests {
		if got := formatTokens(in); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

// A sub-cent cost must not round to $0.00, which reads as free.
func TestFormatUSD(t *testing.T) {
	t.Parallel()

	tests := map[float64]string{
		0: "$0.00", 0.004: "$0.004", 0.42: "$0.42", 12.5: "$12.50",
	}

	for in, want := range tests {
		if got := formatUSD(in); got != want {
			t.Errorf("formatUSD(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := map[time.Duration]string{
		0:                           "",
		-time.Second:                "",
		45 * time.Second:            "45s",
		90 * time.Second:            "1m30s",
		22 * time.Minute:            "22m00s",
		2*time.Hour + 5*time.Minute: "2h05m",
	}

	for in, want := range tests {
		if got := formatDuration(in); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	t.Parallel()

	tests := map[float64]string{0: "0%", 0.256: "26%", 1: "100%", -1: "0%", 5: "100%"}

	for in, want := range tests {
		if got := formatPercent(in); got != want {
			t.Errorf("formatPercent(%v) = %q, want %q", in, got, want)
		}
	}
}

// Cache reads are never also counted as input tokens, so the rate is a real
// ratio rather than a double count.
func TestCacheRate(t *testing.T) {
	t.Parallel()

	u := UsageMsg{InputTokens: 100, CacheReadTokens: 900}

	got, ok := u.CacheRate()
	if !ok || got != 0.9 {
		t.Fatalf("CacheRate() = (%v, %v), want (0.9, true)", got, ok)
	}

	if _, ok := (UsageMsg{}).CacheRate(); ok {
		t.Error("CacheRate claimed to know a rate with no tokens")
	}

	// Nil-safe: usage is absent until the first turn reports.
	var absent *UsageMsg
	if _, ok := absent.cacheRate(); ok {
		t.Error("nil usage reported a cache rate")
	}

	if got := absent.tokenSummary(); got != "" {
		t.Errorf("nil usage token summary = %q", got)
	}
}

func TestEditStatsSummary(t *testing.T) {
	t.Parallel()

	if got := (EditStatsMsg{}).summary(); got != "" {
		t.Errorf("untouched summary = %q, want empty", got)
	}

	got := EditStatsMsg{LinesRead: 4820, LinesAdded: 612, LinesRemoved: 148}.summary()
	for _, want := range []string{"4.8k", "+612", "-148"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
}
