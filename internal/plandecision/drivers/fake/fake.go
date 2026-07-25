// Package fake implements the plandecision.Resolver test double: a
// go-testing.md-mandated fake, not a mock. A test pre-programs the
// Decision (or error) each Resolve call returns — either one scripted
// response per call via a queue, or a single response returned for every
// call — so a plan-gate consumer can be exercised against every Decision
// shape (ALLOW, DENY, each PlanDecisionScope, a CorrectedInput, an error,
// a context-cancellation scenario) without a frontend, a real resolver,
// or any generated mock machinery.
package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pluggableharness/agent/internal/plandecision"
)

// ErrExhausted is returned once a queued Resolver has handed out every
// scripted response. Running off the end of the script is a test-setup
// mistake, so it surfaces as an error rather than silently repeating the
// last response.
var ErrExhausted = errors.New("plandecision/fake: no scripted response left")

// Response is one scripted Resolve outcome. Decision is returned when Err
// is nil; otherwise Err is returned with a zero Decision, exactly as a
// real Resolver would.
type Response struct {
	Decision plandecision.Decision
	Err      error
}

// Resolver is the scripted plandecision.Resolver. Construct it with New
// (a per-call queue) or NewAlways (one response for every call); the zero
// value is a Resolver with an empty queue, which fails every call with
// ErrExhausted.
type Resolver struct {
	mu      sync.Mutex
	queue   []Response
	always  *Response
	calls   []plandecision.Request
	resolve int
}

// New returns a Resolver that hands out responses in order, one per
// Resolve call, then fails subsequent calls with ErrExhausted.
func New(responses ...Response) *Resolver {
	return &Resolver{queue: append([]Response(nil), responses...)}
}

// NewAlways returns a Resolver that returns resp for every Resolve call,
// however many there are — the shape most consumer tests want when the
// verdict itself isn't what's under test.
func NewAlways(resp Response) *Resolver {
	return &Resolver{always: &resp}
}

// Resolve returns the next scripted response, recording req for later
// inspection via Calls. It honors ctx cancellation before consulting the
// script, so a consumer's generic cancellation test behaves the same
// against this fake as against a real Resolver.
func (r *Resolver) Resolve(ctx context.Context, req plandecision.Request) (plandecision.Decision, error) {
	if err := ctx.Err(); err != nil {
		return plandecision.Decision{}, fmt.Errorf("plandecision/fake: resolve: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, req)

	if r.always != nil {
		return r.always.Decision, r.always.Err
	}

	if r.resolve >= len(r.queue) {
		return plandecision.Decision{}, fmt.Errorf("plandecision/fake: resolve call %d: %w", r.resolve+1, ErrExhausted)
	}
	resp := r.queue[r.resolve]
	r.resolve++
	return resp.Decision, resp.Err
}

// Calls returns a copy of every Request passed to Resolve so far, in
// order, including calls that returned an error.
func (r *Resolver) Calls() []plandecision.Request {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]plandecision.Request, len(r.calls))
	copy(out, r.calls)
	return out
}

// Reset clears the recorded calls and rewinds the queue to its first
// scripted response.
func (r *Resolver) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = nil
	r.resolve = 0
}

var _ plandecision.Resolver = (*Resolver)(nil)
