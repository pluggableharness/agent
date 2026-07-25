package sessionscope

import (
	"sort"
	"sync"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Key identifies the plugin a grant belongs to. It mirrors the producer
// identity a kernel-side callback server is bound to, not the agent.hcl
// local name — the callback connection is the only thing that
// establishes who is actually calling (kernel-callbacks.md#the-callback-channel).
type Key struct {
	Category commonv1.Category
	Name     string
}

// KeyFor derives a Key from a producer identity, ignoring version — a
// plugin's version cannot change within one running process, and this
// project's v1 config forbids two concurrently-loaded builds of the
// same category+name (configuration/blocks-reference.md#required_providers
// rules out provider aliasing).
func KeyFor(p *commonv1.ProducerRef) Key {
	return Key{
		Category: p.GetCategory(),
		Name:     p.GetName(),
	}
}

// Registry is the process-wide grant table. The zero value is not
// usable — construct with NewRegistry. Safe for concurrent use.
//
// A sync.RWMutex guards grants: Grant and a release both mutate the
// table and take the write lock; Authorized and Sessions only read it
// and take the read lock, allowing concurrent lookups (the expected
// common case — many RPC handlers checking authorization) to proceed
// without serializing on each other.
type Registry struct {
	mu     sync.RWMutex
	grants map[Key]map[string]int // plugin -> session id -> outstanding grant count
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		grants: make(map[Key]map[string]int),
	}
}

// Grant authorizes key to make session-scoped callbacks naming
// sessionID, and returns the release function that revokes this one
// grant. Grants nest: N calls to Grant for the same (key, sessionID)
// require N releases before authorization is actually withdrawn — this
// is what makes two concurrent tool calls from one plugin into one
// session safe (each call takes its own grant and releases only its own).
// release is idempotent: calling it more than once has no additional
// effect beyond the first call.
func (r *Registry) Grant(key Key, sessionID string) (release func()) {
	r.mu.Lock()
	sessions, ok := r.grants[key]
	if !ok {
		sessions = make(map[string]int)
		r.grants[key] = sessions
	}
	sessions[sessionID]++
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.release(key, sessionID)
		})
	}
}

// release decrements the outstanding grant count for (key, sessionID),
// cleaning up the inner map entry once it reaches zero and the outer
// map entry once its inner map becomes empty, so a fully-released
// Registry holds no stale zero-count entries.
func (r *Registry) release(key Key, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sessions, ok := r.grants[key]
	if !ok {
		return
	}
	sessions[sessionID]--
	if sessions[sessionID] <= 0 {
		delete(sessions, sessionID)
	}
	if len(sessions) == 0 {
		delete(r.grants, key)
	}
}

// Authorized reports whether key currently holds at least one
// outstanding grant for sessionID.
func (r *Registry) Authorized(key Key, sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.grants[key][sessionID] > 0
}

// Sessions returns key's currently-granted session ids, sorted
// (deterministic order for diagnostics/logging by a caller — never used
// by this package itself to pick a session on anyone's behalf).
func (r *Registry) Sessions(key Key) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sessions := r.grants[key]
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
