package shell

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	// minMeterBar is the shortest fill bar worth drawing. Below it the bar says
	// less than the percentage printed beside it, so the segment drops the bar
	// and keeps the number.
	minMeterBar = 8
	// minContextSegment reserves the label, the absolute figures, the
	// percentage, and a bar of at least minMeterBar.
	//
	// Reserving room for the bar rather than just the text is what keeps the
	// line stable while a terminal is resized: without it, a right-hand field
	// becoming affordable could take the meter from drawable to
	// below-the-minimum in a single column, so the bar blinked out and back as
	// the window moved.
	minContextSegment = 36
)

// formatPercent renders a 0..1 fraction as a whole percentage.
func formatPercent(f float64) string {
	return strconv.Itoa(int(math.Round(math.Min(math.Max(f, 0), 1)*100))) + "%"
}

// formatTokens abbreviates a token count: exact below a thousand, then k, then
// M. A status bar has no room for nine digits, and nobody reads them anyway.
func formatTokens(n int64) string {
	switch {
	case n < 0:
		return "0"
	case n < 1_000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimZero(float64(n)/1_000) + "k"
	default:
		return trimZero(float64(n)/1_000_000) + "M"
	}
}

// trimZero renders one decimal place, dropping it when it is zero, so counts
// read as "18.2k" and "5k" rather than "5.0k".
func trimZero(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)

	return strings.TrimSuffix(s, ".0")
}

// formatUSD renders a cost. Sub-cent amounts get a third decimal rather than
// rounding to $0.00, which would read as free.
func formatUSD(v float64) string {
	if v > 0 && v < 0.01 {
		return fmt.Sprintf("$%.3f", v)
	}

	return fmt.Sprintf("$%.2f", v)
}

// formatDuration renders elapsed time at the coarsest useful precision.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}

	d = d.Round(time.Second)

	h := int(d.Hours())
	mn := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, mn)
	}

	if mn > 0 {
		return fmt.Sprintf("%dm%02ds", mn, sec)
	}

	return strconv.Itoa(sec) + "s"
}

// summary renders the read/changed line counts, or empty when nothing has been
// touched yet.
func (e EditStatsMsg) summary() string {
	if e.LinesRead == 0 && e.LinesAdded == 0 && e.LinesRemoved == 0 {
		return ""
	}

	return fmt.Sprintf("%s read +%s -%s",
		formatTokens(e.LinesRead), formatTokens(e.LinesAdded), formatTokens(e.LinesRemoved))
}

// tokenSummary renders the write/cache-write/read split. It is nil-safe
// because usage is absent until the first turn reports.
func (u *UsageMsg) tokenSummary() string {
	if u == nil {
		return ""
	}

	return fmt.Sprintf("%s out %s in %s cw",
		formatTokens(u.OutputTokens), formatTokens(u.InputTokens+u.CacheReadTokens), formatTokens(u.CacheWriteTokens))
}

// cacheRate is the nil-safe form of UsageMsg.CacheRate.
func (u *UsageMsg) cacheRate() (float64, bool) {
	if u == nil {
		return 0, false
	}

	return u.CacheRate()
}

// tokens renders one token count from usage, nil-safe, or empty when there is
// no usage yet or the count is zero.
func (u *UsageMsg) tokens(pick func(*UsageMsg) int64) string {
	if u == nil {
		return ""
	}

	n := pick(u)
	if n <= 0 {
		return ""
	}

	return formatTokens(n)
}
