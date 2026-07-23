package producer

import (
	"context"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// contextKey is this package's context key for producer attribution — an
// unexported struct type, per this project's context-key convention, so it
// can never collide with a key defined elsewhere.
type contextKey struct{}

// WithProducer returns a context carrying the calling plugin's
// server-derived identity, for attaching producer attribution to
// downstream calls. Producer identity MUST be set by trusted, kernel-side
// code only (kernel-callbacks.md §4/§5: server-derived, never
// client-supplied) — never populated from an untrusted request field.
func WithProducer(ctx context.Context, p *commonv1.ProducerRef) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}

// FromContext retrieves the producer identity set by WithProducer, if any.
func FromContext(ctx context.Context) (*commonv1.ProducerRef, bool) {
	p, ok := ctx.Value(contextKey{}).(*commonv1.ProducerRef)
	return p, ok
}
