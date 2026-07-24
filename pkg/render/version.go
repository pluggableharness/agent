package render

import (
	"fmt"
	"sync"

	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

// VersionedRenderer decodes one specific schema_version's opaque payload
// bytes into a RenderTree. A plugin registers one VersionedRenderer per
// schema_version it has ever emitted
// (docs/specifications/frontend/render-tree.md#schema-versioning) with a
// VersionRegistry, and keeps every past version's VersionedRenderer
// registered for as long as any retained session can still reference it —
// the same permanence guarantee the wire message shapes themselves carry.
type VersionedRenderer func(payload []byte) (*renderv1.RenderTree, error)

// VersionRegistry dispatches a RenderRequest's schema_version to the
// VersionedRenderer a plugin registered for it, so a plugin's Render
// implementation branches on schema_version (as
// docs/specifications/frontend/render-tree.md#schema-versioning requires)
// instead of every plugin author hand-rolling that dispatch.
//
// A VersionRegistry is safe for concurrent use: Register is expected to
// run a handful of times at process start (one call per schema_version a
// plugin has ever shipped) before Render starts serving live/replayed
// RenderRequests, but guarding the map with a sync.RWMutex rather than
// requiring an explicit "freeze" step keeps VersionRegistry
// misuse-resistant — a late Register (e.g. a plugin that loads version
// decoders lazily) or a concurrent Register from a background goroutine
// stays safe instead of racing readers. Register calls are rare and cheap,
// so the lock's overhead is negligible next to the safety it buys.
type VersionRegistry struct {
	mu        sync.RWMutex
	renderers map[string]VersionedRenderer
}

// NewVersionRegistry builds an empty VersionRegistry.
func NewVersionRegistry() *VersionRegistry {
	return &VersionRegistry{renderers: make(map[string]VersionedRenderer)}
}

// Register associates a schema_version string with the VersionedRenderer
// that decodes payloads of that shape. Registering the same version twice
// replaces the previously registered VersionedRenderer.
func (r *VersionRegistry) Register(version string, fn VersionedRenderer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renderers[version] = fn
}

// Render dispatches to the VersionedRenderer registered for
// schemaVersion and returns its result. It returns ErrUnknownSchemaVersion,
// wrapped with the requested version, if nothing is registered for
// schemaVersion — including when the registry is empty.
func (r *VersionRegistry) Render(schemaVersion string, payload []byte) (*renderv1.RenderTree, error) {
	r.mu.RLock()
	fn, ok := r.renderers[schemaVersion]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("render: schema version %q: %w", schemaVersion, ErrUnknownSchemaVersion)
	}
	tree, err := fn(payload)
	if err != nil {
		return nil, fmt.Errorf("render: schema version %q: %w", schemaVersion, err)
	}
	return tree, nil
}
