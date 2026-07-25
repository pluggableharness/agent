package sessionstate

import (
	"context"
	"iter"

	"github.com/pluggableharness/agent/internal/statebackend"
)

// Meta, TotalCostUSD, and Events below are thin, unlocked read
// pass-throughs to the underlying *statebackend.Session — added so a
// caller like internal/kernelcallback's GetSession/ReadEvents RPC
// handlers can read an authorized live session's persisted state without
// reaching around this package's sole-writer abstraction to import
// internal/statebackend directly (this package's own doc comment already
// forbids internal/kernelcallback importing internal/statebackend
// instead). Unlike every Emit* method in emit.go, none of these take
// l.mu: they're read-only, and sqlite's own WAL-mode readers see either
// the state before or after a concurrent write's commit, never a partial
// one, so a read needs no additional serialization against Live's
// single-writer lock.

// Meta returns this session's persisted session_meta row
// (state-backend.md's session_meta table).
func (l *Live) Meta(ctx context.Context) (statebackend.SessionMeta, error) {
	return l.session.Meta(ctx)
}

// TotalCostUSD returns this session's persisted running total spend —
// SUM(cost_ledger.cost_usd) over every cost_ledger row this session's file
// holds.
func (l *Live) TotalCostUSD(ctx context.Context) (float64, error) {
	return l.session.TotalCostUSD(ctx)
}

// Events returns this session's persisted events matching q, in
// sequence-ascending order (determinism.md — never by time).
func (l *Live) Events(ctx context.Context, q statebackend.EventQuery) iter.Seq2[statebackend.Event, error] {
	return l.session.EventsMatching(ctx, q)
}
