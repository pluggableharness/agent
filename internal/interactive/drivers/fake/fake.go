// Package fake is a scripted interactive.Resolver for tests — pre-program
// a Response or an error to return, so a consumer (the future tool
// scheduler) can exercise both the "human answered" and "no frontend"
// paths without depending on drivers/unattended or on a real frontend.
//
// It is hand-written rather than generated, per .claude/rules/go-testing.md:
// a fake is a small real implementation, not a mock with call recording
// helpers. It does record the requests it was handed, because a consumer
// asserting "the scheduler asked the human exactly this" is the main
// thing this fake exists to make possible.
package fake

import (
	"context"
	"sync"

	"github.com/pluggableharness/agent/internal/interactive"
)

// Resolver returns a scripted Response and/or error for every Resolve
// call, and records the requests it received. The zero value is usable:
// it answers every call with a zero Response and a nil error.
//
// Safe for concurrent use, even though interactive calls execute
// sequentially by spec (tool/protocol.md#kind-interactive) — a fake that
// races under `go test -race` would be a worse debugging experience than
// one mutex.
type Resolver struct {
	// Response is returned from every Resolve call when Err is nil.
	Response interactive.Response

	// Err, when non-nil, is returned from every Resolve call instead of
	// Response — set it to interactive.ErrNoFrontend to script the
	// unattended path without importing drivers/unattended.
	Err error

	mu       sync.Mutex
	requests []interactive.Request
}

// Compile-time anchor: the fake implements the parent seam.
var _ interactive.Resolver = (*Resolver)(nil)

// New returns a Resolver scripted to answer every call with resp, or
// with err when err is non-nil.
func New(resp interactive.Response, err error) *Resolver {
	return &Resolver{Response: resp, Err: err}
}

// Resolve records req and returns the scripted answer. It honors ctx
// cancellation the same way any real Resolver must: an already-done ctx
// returns ctx.Err() and records nothing.
func (r *Resolver) Resolve(ctx context.Context, req interactive.Request) (interactive.Response, error) {
	if err := ctx.Err(); err != nil {
		return interactive.Response{}, err
	}

	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()

	if r.Err != nil {
		return interactive.Response{}, r.Err
	}
	return r.Response, nil
}

// Requests returns a copy of every Request passed to Resolve, in call
// order.
func (r *Resolver) Requests() []interactive.Request {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]interactive.Request, len(r.requests))
	copy(out, r.requests)
	return out
}
