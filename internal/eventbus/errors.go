package eventbus

import "errors"

// ErrClosed is returned by Publish or Subscribe when called after the Bus
// (or, for a Subscription-scoped operation, the Subscription itself) has
// been closed.
var ErrClosed = errors.New("eventbus: closed")

// ErrEmptyTopic is returned by Subscribe when topic is the empty string.
var ErrEmptyTopic = errors.New("eventbus: topic is required")

// ErrNilHandler is returned by Subscribe when handler is nil.
var ErrNilHandler = errors.New("eventbus: handler is required")
