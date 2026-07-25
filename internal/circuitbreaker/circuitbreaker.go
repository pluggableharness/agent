package circuitbreaker

import "sync"

// Config is the trip thresholds for one Breaker. Both thresholds are
// independent — either one crossing trips the breaker for that provider. A
// zero value for either sub-threshold disables that specific check
// (ConsecutiveThreshold=0 means "never trip on consecutive count alone",
// relying only on the window count, and vice versa; both zero means this
// breaker never trips, which is a legal, if pointless, configuration — not
// an error).
type Config struct {
	// ConsecutiveThreshold trips after this many consecutive bad events
	// for the same provider, with no good event breaking the streak.
	ConsecutiveThreshold int

	// WindowSize is the number of most-recent events (good or bad) per
	// provider to consider for the window-based check.
	WindowSize int

	// WindowThreshold trips when at least this many of the most recent
	// WindowSize events for a provider were bad.
	WindowThreshold int
}

// providerState is the mutable per-provider tracking state: the current
// consecutive-bad-event streak, and a fixed-size ring buffer of the last
// WindowSize events (true = bad) plus a running count of how many of them
// are bad, maintained incrementally so a trip check never has to rescan
// the window.
type providerState struct {
	consecutive int

	window    []bool
	windowPos int
	windowLen int
	windowBad int
}

// Breaker tracks denial/crash counts per provider name and reports when
// either configured threshold is crossed. One Breaker instance is scoped
// to one session (a fresh Breaker per session, per
// plan-apply-gate.md#circuit-breaker-on-repeated-denials' "within one
// session" framing) and shared by both the plan/apply gate (denials) and
// the tool scheduler (crashes) — a provider that both gets denied AND
// crashes contributes to the SAME per-provider counters. See doc.go for
// the reasoning behind that shared-signal design.
type Breaker struct {
	cfg Config

	mu        sync.Mutex
	providers map[string]*providerState
}

// New returns a Breaker configured per cfg. Safe for concurrent use —
// RecordDenial/RecordCrash/RecordSuccess/Reset may be called from
// goroutines running concurrent tool calls within one turn.
func New(cfg Config) *Breaker {
	return &Breaker{
		cfg:       cfg,
		providers: make(map[string]*providerState),
	}
}

// RecordDenial records one policy-denial event for provider and reports
// whether either threshold is now crossed for that provider.
func (b *Breaker) RecordDenial(provider string) (tripped bool) {
	return b.record(provider, true)
}

// RecordCrash records one plugin-crash event for provider and reports
// whether either threshold is now crossed for that provider.
func (b *Breaker) RecordCrash(provider string) (tripped bool) {
	return b.record(provider, true)
}

// RecordSuccess records one non-denial, non-crash event for provider —
// this breaks a consecutive-bad-event streak and slides the window
// forward with a "good" entry. A caller SHOULD call this on every
// successful tool invocation for a provider so the consecutive counter
// resets correctly; without it, a single denial early in a long healthy
// session would never be forgotten.
func (b *Breaker) RecordSuccess(provider string) {
	b.record(provider, false)
}

// Reset clears all tracked state for provider — e.g. after a trip has been
// handled via the caller's limit-reached path and the caller wants to give
// the provider a fresh chance.
func (b *Breaker) Reset(provider string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.providers, provider)
}

// record applies one event (bad or good) for provider and reports whether
// either configured threshold is now crossed. It is the shared
// implementation behind RecordDenial, RecordCrash, and RecordSuccess — see
// doc.go for why denials and crashes are not tracked as separate signals.
func (b *Breaker) record(provider string, bad bool) (tripped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st := b.providers[provider]
	if st == nil {
		st = &providerState{}
		if b.cfg.WindowSize > 0 {
			st.window = make([]bool, b.cfg.WindowSize)
		}
		b.providers[provider] = st
	}

	if bad {
		st.consecutive++
	} else {
		st.consecutive = 0
	}

	if b.cfg.WindowSize > 0 {
		slideWindow(st, bad, b.cfg.WindowSize)
	}

	if b.cfg.ConsecutiveThreshold > 0 && st.consecutive >= b.cfg.ConsecutiveThreshold {
		tripped = true
	}
	if b.cfg.WindowSize > 0 && b.cfg.WindowThreshold > 0 && st.windowBad >= b.cfg.WindowThreshold {
		tripped = true
	}
	return tripped
}

// slideWindow pushes one event (bad or good) into st's fixed-size ring
// buffer, evicting the oldest entry once the buffer is full, and keeps
// st.windowBad in sync with the buffer's contents incrementally rather
// than recounting it on every call.
func slideWindow(st *providerState, bad bool, size int) {
	if st.windowLen < size {
		st.window[st.windowPos] = bad
		if bad {
			st.windowBad++
		}
		st.windowLen++
	} else {
		evicted := st.window[st.windowPos]
		if evicted != bad {
			if evicted {
				st.windowBad--
			} else {
				st.windowBad++
			}
		}
		st.window[st.windowPos] = bad
	}
	st.windowPos = (st.windowPos + 1) % size
}
