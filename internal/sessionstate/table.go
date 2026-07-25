package sessionstate

import "sync"

// Table is the process-wide registry of currently-live sessions, keyed by
// session id. Constructed once at kernel startup. Safe for concurrent use;
// its mutex guards only the map itself, never an individual *Live's own
// operations (each Live serializes its own Emit/EmitMessage/EmitPlan calls
// independently via its own mutex).
type Table struct {
	mu       sync.RWMutex
	sessions map[string]*Live
}

// NewTable returns an empty, ready-to-use Table.
func NewTable() *Table {
	return &Table{sessions: make(map[string]*Live)}
}

// Put registers live under sessionID, replacing any existing entry — the
// caller is responsible for not double-registering a still-active session
// (e.g. Closing and Removing the previous entry first, if one exists);
// this method does not defensively check for one, beyond what the map
// assignment gives for free.
func (t *Table) Put(sessionID string, live *Live) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessions[sessionID] = live
}

// Get returns the live session for sessionID, if currently registered.
func (t *Table) Get(sessionID string) (*Live, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	live, ok := t.sessions[sessionID]
	return live, ok
}

// Remove unregisters sessionID (called after session-end, once its Live
// has been Closed).
func (t *Table) Remove(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, sessionID)
}
