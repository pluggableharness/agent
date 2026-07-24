package plugin

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/go-plugin"

	"github.com/pluggableharness/agent/pkg/kernel"
)

// Callback is a lazily-dialed handle to the kernel callback channel
// (pkg/kernel.Client). The zero value is not usable; construct one with
// NewCallback. A Callback returned by NewCallback is not yet connected to
// anything — see doc.go's "callback-timing trap" for why the dial must be
// deferred until a plugin author's own RPC handler calls Client, rather
// than happening at construction or from within Serve's internal
// GRPCServer method. Safe for concurrent use.
type Callback struct {
	mu     sync.Mutex
	broker *plugin.GRPCBroker

	once   sync.Once
	client *kernel.Client
	err    error

	// dial is kernel.Dial by default; overridden in tests so the
	// sync.Once guard can be exercised without a real *plugin.GRPCBroker
	// (kernel.Dial's own doc comment explains why one can't be
	// constructed from outside hashicorp/go-plugin for a test).
	dial func(*plugin.GRPCBroker) (*kernel.Client, error)
}

// NewCallback returns a Callback not yet connected to anything. Construct
// one and hold onto it in Config.Callback; Serve's internal GRPCPlugin
// adapter records the broker on it once GRPCServer runs.
func NewCallback() *Callback {
	return &Callback{dial: kernel.Dial}
}

// setBroker records broker for a later Client call. Called exactly once
// per plugin process, from within Serve's internal GRPCPlugin adapter's
// GRPCServer method, immediately after go-plugin hands that method a
// broker for this launch (serve.go).
func (c *Callback) setBroker(broker *plugin.GRPCBroker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broker = broker
}

// Client dials the fixed callback broker ID on first call (sync.Once) and
// returns the resulting *kernel.Client on every call thereafter, including
// a subsequent call after a failed dial (the dial is not retried; a
// failed dial is a permanent condition for the process's lifetime, same
// as a failed handshake would be). Must be called only after go-plugin has
// begun dispensing this process's client to the kernel (i.e. from within a
// Service's own RPC handler, never from inside GRPCServer itself) — see
// doc.go's "callback-timing trap". The context parameter is unused —
// kernel.Dial (and *plugin.GRPCBroker.Dial underneath it) has no
// context-aware variant — and is kept unnamed rather than named ctx so it
// doesn't read as though cancellation is honored; it exists purely so this
// method matches the blocking-call signature convention every other
// method in this SDK follows.
func (c *Callback) Client(context.Context) (*kernel.Client, error) {
	c.once.Do(func() {
		c.mu.Lock()
		broker := c.broker
		c.mu.Unlock()

		if broker == nil {
			c.err = errCallbackBrokerUnset
			return
		}
		c.client, c.err = c.dial(broker)
	})
	if c.err != nil {
		return nil, fmt.Errorf("plugin: callback client: %w", c.err)
	}
	return c.client, nil
}
