package statebackend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// timestampLayout is the RFC 3339, UTC, millisecond-precision layout every
// TEXT timestamp column in the schema is stored and parsed with
// (docs/specifications/state-backend.md#ordering--concurrency: timestamps
// are display-only, never ordering-authoritative, but still need a single
// fixed format for round-tripping).
const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

// formatTimestamp renders t as UTC RFC 3339 with millisecond precision, the
// canonical on-disk form for every TEXT timestamp column in the schema.
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}

// parseTimestamp parses a TEXT timestamp column back into a time.Time,
// inverse of formatTimestamp.
func parseTimestamp(s string) (time.Time, error) {
	return time.Parse(timestampLayout, s)
}

// sessionStatusText maps SessionStatus to the exact lowercase snake_case
// text docs/specifications/state-backend.md#session_meta's status column
// documents ("running | completed | error_max_turns | error_max_budget_usd
// | error_max_wall_clock | cancelled | failed") — the wire enum's own
// SCREAMING_SNAKE_CASE String() is not what gets stored.
var sessionStatusText = map[sessionv1.SessionStatus]string{
	sessionv1.SessionStatus_SESSION_STATUS_RUNNING:              "running",
	sessionv1.SessionStatus_SESSION_STATUS_COMPLETED:            "completed",
	sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_TURNS:      "error_max_turns",
	sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_BUDGET_USD: "error_max_budget_usd",
	sessionv1.SessionStatus_SESSION_STATUS_ERROR_MAX_WALL_CLOCK: "error_max_wall_clock",
	sessionv1.SessionStatus_SESSION_STATUS_CANCELLED:            "cancelled",
	sessionv1.SessionStatus_SESSION_STATUS_FAILED:               "failed",
}

// sessionTextStatus is sessionStatusText inverted, built once at package
// init time from sessionStatusText itself so the two can never drift.
var sessionTextStatus = func() map[string]sessionv1.SessionStatus {
	m := make(map[string]sessionv1.SessionStatus, len(sessionStatusText))
	for status, text := range sessionStatusText {
		m[text] = status
	}
	return m
}()

// encodeSessionStatus renders status as its stored TEXT representation.
// SESSION_STATUS_UNSPECIFIED and any unrecognized value are rejected — like
// EventKind's zero value (docs/specifications/state-backend.md#the-kind-enum),
// SessionStatus's zero value MUST NOT ever be persisted.
func encodeSessionStatus(status sessionv1.SessionStatus) (string, error) {
	text, ok := sessionStatusText[status]
	if !ok {
		return "", fmt.Errorf("statebackend: session status %v has no stored representation", status)
	}
	return text, nil
}

// decodeSessionStatus is the inverse of encodeSessionStatus, used when
// reading a session_meta row back.
func decodeSessionStatus(text string) (sessionv1.SessionStatus, error) {
	status, ok := sessionTextStatus[text]
	if !ok {
		return sessionv1.SessionStatus_SESSION_STATUS_UNSPECIFIED, fmt.Errorf("statebackend: unrecognized session status %q", text)
	}
	return status, nil
}

// SessionMeta mirrors the session_meta table's columns
// (docs/specifications/state-backend.md#session_meta) — the one table that
// isn't append-only, updated in place as a session progresses.
// ParentSessionID is empty for a root session (the column's SQL NULL);
// EndedAt is nil while the session is still running (the column's SQL
// NULL).
type SessionMeta struct {
	// SessionID matches the filename stem: a canonical ULID (NewSessionID).
	SessionID string
	// ParentSessionID is empty for a root session.
	ParentSessionID string
	// Profile is the agent profile this session was started with.
	Profile string
	// Status is the session's current lifecycle state.
	Status sessionv1.SessionStatus
	// Depth is the cached depth-budget value
	// (docs/specifications/agent-loop/subagents.md#depth-limits), avoiding
	// a parent-chain walk when scanning.
	Depth int
	// StartedAt is when the session began.
	StartedAt time.Time
	// EndedAt is nil while the session is still running.
	EndedAt *time.Time
}

// Session is an open handle to one session's sqlite file: the *sql.DB
// backing it, its identifying ids, and the logger/telemetry provider it
// instruments through (inherited from the Store that opened it). Append and
// query methods live in session.go (event/cost/plan-item writes) and
// query.go (Stage 3's replay reads).
type Session struct {
	id        string
	db        *sql.DB
	path      string
	logger    *slog.Logger
	telemetry *telemetry.Provider

	// closed is set by Close so every write method can reject a call made
	// after it with ErrClosed instead of surfacing a raw database/sql
	// error — see session.go.
	closed atomic.Bool
}

// ID returns the session's ULID.
func (s *Session) ID() string {
	return s.id
}

// Store manages the directory of per-session sqlite files described by
// docs/specifications/state-backend.md#file-layout
// ($XDG_STATE_HOME/agent/sessions/<session_id>.sqlite). It is not itself a
// database handle — each Session opened through it owns its own *sql.DB.
type Store struct {
	dir       string
	clock     func() time.Time
	logger    *slog.Logger
	telemetry *telemetry.Provider
}

// Option configures a Store constructed by NewStore.
type Option func(*Store)

// WithClock overrides the Store's source of the current time, used to
// default SessionMeta.StartedAt when Create is called without one. Tests
// use this for a deterministic clock; production code leaves it unset,
// defaulting to time.Now.
func WithClock(clock func() time.Time) Option {
	return func(s *Store) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithLogger sets the *slog.Logger the Store logs through. A nil logger (or
// omitting this option) leaves the default of slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(s *Store) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithTelemetry sets the *telemetry.Provider the Store and every Session it
// opens instrument through (internal/telemetry/span.go's
// StartStateBackend* helpers). Omitting this option (or passing nil)
// leaves the default: a Provider with every signal disabled, so New wires
// OTel's own no-op tracer/meter/logger providers directly
// (internal/telemetry.New's documented behavior for a disabled signal) —
// the instrumentation code path still runs on every call, at effectively
// zero cost, rather than being conditionally skipped.
func WithTelemetry(prov *telemetry.Provider) Option {
	return func(s *Store) {
		if prov != nil {
			s.telemetry = prov
		}
	}
}

// defaultTelemetryProvider builds the Provider a Store falls back to when
// WithTelemetry isn't supplied. Every signal is disabled, so
// telemetry.New never calls into the noop.Backend passed here at all — it
// exists only to satisfy New's non-nil Backend requirement. Constructing
// this at NewStore time (rather than propagating a caller context, which
// NewStore's signature has none of) is this package's one ingress-style
// use of context.Background(), the same carve-out go-style.md gives
// "main, an HTTP handler boundary, a message-consume loop": NewStore is
// this package's construction entry point.
func defaultTelemetryProvider() (*telemetry.Provider, error) {
	return telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
}

// NewStore returns a Store rooted at dir, creating it (mode 0700) if it
// does not already exist. dir is expected to be
// $XDG_STATE_HOME/agent/sessions per
// docs/specifications/state-backend.md#file-layout, but NewStore itself
// takes the resolved path rather than reading XDG env vars — that
// resolution belongs to the caller doing kernel-wide path setup.
func NewStore(dir string, opts ...Option) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("statebackend: new store: dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("statebackend: new store: %w", err)
	}

	st := &Store{dir: dir, clock: time.Now, logger: slog.Default()}
	for _, opt := range opts {
		opt(st)
	}
	if st.telemetry == nil {
		prov, err := defaultTelemetryProvider()
		if err != nil {
			return nil, fmt.Errorf("statebackend: new store: %w", err)
		}
		st.telemetry = prov
	}
	return st, nil
}

// sessionPath returns the on-disk path for sessionID's file, per
// docs/specifications/state-backend.md#file-layout.
func (st *Store) sessionPath(sessionID string) string {
	return filepath.Join(st.dir, sessionID+".sqlite")
}

// openDB opens the sqlite file at path for exclusive, sole-writer access
// (docs/specifications/state-backend.md#ordering--concurrency: the kernel
// is the only writer to any given session's file). The returned *sql.DB is
// capped at one open connection, and WAL mode plus foreign key enforcement
// are set immediately after connecting.
func openDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("statebackend: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("statebackend: open %s: set journal_mode: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("statebackend: open %s: set foreign_keys: %w", path, err)
	}
	return db, nil
}

// Create creates a new session file for meta.SessionID
// (docs/specifications/state-backend.md#file-layout), applies the
// five-table schema, stamps PRAGMA user_version, and inserts meta as the
// initial session_meta row. meta.SessionID MUST already be a valid,
// caller-generated ULID (see NewSessionID) — Create does not generate one
// itself. If meta.StartedAt is zero, it defaults to the Store's clock. Any
// failure after the file is created (a bad schema, an invalid
// meta.Status, ...) removes the partial file rather than leaving it behind
// to block a retry with the same session ID.
func (st *Store) Create(ctx context.Context, meta SessionMeta) (_ *Session, err error) {
	ctx, span := st.telemetry.StartStateBackendSessionCreate(ctx, meta.SessionID)
	defer func() { telemetry.EndSpan(span, err) }()

	if err = ValidateSessionID(meta.SessionID); err != nil {
		err = fmt.Errorf("statebackend: create: %w", err)
		return nil, err
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = st.clock()
	}

	path := st.sessionPath(meta.SessionID)
	st.logger.DebugContext(ctx, "statebackend: creating session", "session_id", meta.SessionID)

	// #nosec G304 -- path is built from meta.SessionID, which ValidateSessionID above already rejected unless it's a canonical ULID; it is never attacker-controlled arbitrary input.
	f, openErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if openErr != nil {
		if errors.Is(openErr, fs.ErrExist) {
			err = fmt.Errorf("statebackend: create %s: session file already exists", meta.SessionID)
			return nil, err
		}
		err = fmt.Errorf("statebackend: create %s: %w", meta.SessionID, openErr)
		return nil, err
	}
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(path)
		err = fmt.Errorf("statebackend: create %s: %w", meta.SessionID, closeErr)
		return nil, err
	}

	sess, popErr := st.populateCreatedFile(ctx, path, meta)
	if popErr != nil {
		_ = os.Remove(path)
		err = fmt.Errorf("statebackend: create %s: %w", meta.SessionID, popErr)
		return nil, err
	}
	return sess, nil
}

// populateCreatedFile opens the just-created, still-empty file at path,
// applies the schema, and inserts meta's session_meta row. Split out of
// Create so every failure path shares one os.Remove(path) cleanup call.
func (st *Store) populateCreatedFile(ctx context.Context, path string, meta SessionMeta) (*Session, error) {
	db, err := openDB(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := initSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := insertSessionMeta(ctx, db, meta); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Session{id: meta.SessionID, db: db, path: path, logger: st.logger, telemetry: st.telemetry}, nil
}

// checkIntegrity is a seam for Stage 3's
// docs/specifications/state-backend.md#corruption-recovery flow
// (PRAGMA integrity_check plus salvage-and-rename on failure). It is a
// no-op in Stage 1/2 so Open's structure does not need to change when
// recovery is added.
func (st *Store) checkIntegrity(_ context.Context, _ string, _ *sql.DB) error {
	return nil
}

// Open opens an existing session file for sessionID. It validates
// sessionID, runs the (currently no-op) corruption-recovery seam, then
// checks PRAGMA user_version before any other operation touches the file
// (docs/specifications/state-backend.md#schema-migration): newer than
// currentSchemaVersion returns ErrSchemaTooNew; older applies the ordered
// migrations slice. sessionID not found returns ErrNotFound.
func (st *Store) Open(ctx context.Context, sessionID string) (_ *Session, err error) {
	ctx, span := st.telemetry.StartStateBackendSessionOpen(ctx, sessionID)
	defer func() { telemetry.EndSpan(span, err) }()

	if err = ValidateSessionID(sessionID); err != nil {
		err = fmt.Errorf("statebackend: open: %w", err)
		return nil, err
	}
	st.logger.DebugContext(ctx, "statebackend: opening session", "session_id", sessionID)

	path := st.sessionPath(sessionID)
	if _, statErr := os.Stat(path); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			err = fmt.Errorf("statebackend: open %s: %w", sessionID, ErrNotFound)
			return nil, err
		}
		err = fmt.Errorf("statebackend: open %s: %w", sessionID, statErr)
		return nil, err
	}

	db, openErr := openDB(ctx, path)
	if openErr != nil {
		err = fmt.Errorf("statebackend: open %s: %w", sessionID, openErr)
		return nil, err
	}

	if icErr := st.checkIntegrity(ctx, path, db); icErr != nil {
		_ = db.Close()
		err = fmt.Errorf("statebackend: open %s: %w", sessionID, icErr)
		return nil, err
	}

	version, verErr := readUserVersion(ctx, db)
	if verErr != nil {
		_ = db.Close()
		err = fmt.Errorf("statebackend: open %s: %w", sessionID, verErr)
		return nil, err
	}
	switch {
	case version > currentSchemaVersion:
		_ = db.Close()
		err = fmt.Errorf("statebackend: open %s: schema version %d: %w", sessionID, version, ErrSchemaTooNew)
		return nil, err
	case version < currentSchemaVersion:
		if migErr := applyMigrations(ctx, db, migrations, version, currentSchemaVersion); migErr != nil {
			_ = db.Close()
			err = fmt.Errorf("statebackend: open %s: %w", sessionID, migErr)
			return nil, err
		}
	}

	return &Session{id: sessionID, db: db, path: path, logger: st.logger, telemetry: st.telemetry}, nil
}

// List returns every session's session_meta row, ordered by session_id
// (docs/specifications/state-backend.md#cross-session-queries: there is no
// separate index, so this scans the store directory). Since session IDs are
// canonical ULIDs, sorting by session_id is equivalent to chronological
// order.
func (st *Store) List(ctx context.Context) ([]SessionMeta, error) {
	return st.scan(ctx, func(SessionMeta) bool { return true })
}

// Children returns every session whose session_meta.parent_session_id is
// sessionID, ordered by session_id
// (docs/specifications/state-backend.md#live-vs-post-hoc-tree-walking: a
// post-hoc query, reconstructing the tree by scanning files).
func (st *Store) Children(ctx context.Context, sessionID string) ([]SessionMeta, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("statebackend: children: %w", err)
	}
	return st.scan(ctx, func(m SessionMeta) bool { return m.ParentSessionID == sessionID })
}

// scan reads every *.sqlite file's session_meta row directly (bypassing
// Open's schema-version-check-and-migrate path — a metadata scan MUST NOT
// have the side effect of migrating every file it merely lists), filters by
// keep, and returns the result ordered by session_id. A file whose name
// isn't a valid session ID is logged at WARN and skipped rather than
// failing the whole scan, since the sessions directory is not guaranteed to
// contain only session files (e.g. future *.sqlite.corrupt entries from
// Stage 3's recovery flow).
func (st *Store) scan(ctx context.Context, keep func(SessionMeta) bool) ([]SessionMeta, error) {
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		return nil, fmt.Errorf("statebackend: scan sessions: %w", err)
	}

	metas := make([]SessionMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sqlite" {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), ".sqlite")
		if err := ValidateSessionID(sessionID); err != nil {
			st.logger.WarnContext(ctx, "statebackend: skipping non-session file", "file", entry.Name(), "err", err)
			continue
		}

		meta, err := st.readSessionMeta(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("statebackend: scan sessions: %s: %w", sessionID, err)
		}
		if keep(meta) {
			metas = append(metas, meta)
		}
	}

	sort.Slice(metas, func(i, j int) bool { return metas[i].SessionID < metas[j].SessionID })
	return metas, nil
}

// readSessionMeta opens sessionID's file directly (not through Open — see
// scan's comment) purely to read its single session_meta row.
func (st *Store) readSessionMeta(ctx context.Context, sessionID string) (SessionMeta, error) {
	db, err := openDB(ctx, st.sessionPath(sessionID))
	if err != nil {
		return SessionMeta{}, err
	}
	defer func() { _ = db.Close() }()

	return querySessionMeta(ctx, db, sessionID)
}

// insertSessionMeta inserts meta as session_meta's single row for a
// freshly created session file.
func insertSessionMeta(ctx context.Context, db *sql.DB, meta SessionMeta) error {
	statusText, err := encodeSessionStatus(meta.Status)
	if err != nil {
		return fmt.Errorf("statebackend: insert session_meta: %w", err)
	}

	var parentSessionID any
	if meta.ParentSessionID != "" {
		parentSessionID = meta.ParentSessionID
	}
	var endedAt any
	if meta.EndedAt != nil {
		endedAt = formatTimestamp(*meta.EndedAt)
	}

	const q = `INSERT INTO session_meta (session_id, parent_session_id, profile, status, depth, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := db.ExecContext(ctx, q,
		meta.SessionID, parentSessionID, meta.Profile, statusText, meta.Depth,
		formatTimestamp(meta.StartedAt), endedAt,
	); err != nil {
		return fmt.Errorf("statebackend: insert session_meta: %w", err)
	}
	return nil
}

// querySessionMeta reads sessionID's session_meta row from db.
func querySessionMeta(ctx context.Context, db *sql.DB, sessionID string) (SessionMeta, error) {
	const q = `SELECT session_id, parent_session_id, profile, status, depth, started_at, ended_at FROM session_meta WHERE session_id = ?`
	row := db.QueryRowContext(ctx, q, sessionID)
	return scanSessionMeta(row)
}

// scanSessionMeta decodes one session_meta row, translating its stored TEXT
// status back to sessionv1.SessionStatus and its TEXT timestamps back to
// time.Time.
func scanSessionMeta(row *sql.Row) (SessionMeta, error) {
	var (
		meta            SessionMeta
		parentSessionID sql.NullString
		statusText      string
		startedAtText   string
		endedAtText     sql.NullString
	)
	if err := row.Scan(&meta.SessionID, &parentSessionID, &meta.Profile, &statusText, &meta.Depth, &startedAtText, &endedAtText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionMeta{}, fmt.Errorf("statebackend: session_meta: %w", ErrNotFound)
		}
		return SessionMeta{}, fmt.Errorf("statebackend: session_meta: %w", err)
	}
	meta.ParentSessionID = parentSessionID.String

	status, err := decodeSessionStatus(statusText)
	if err != nil {
		return SessionMeta{}, fmt.Errorf("statebackend: session_meta: %w", err)
	}
	meta.Status = status

	startedAt, err := parseTimestamp(startedAtText)
	if err != nil {
		return SessionMeta{}, fmt.Errorf("statebackend: session_meta: started_at: %w", err)
	}
	meta.StartedAt = startedAt

	if endedAtText.Valid {
		endedAt, err := parseTimestamp(endedAtText.String)
		if err != nil {
			return SessionMeta{}, fmt.Errorf("statebackend: session_meta: ended_at: %w", err)
		}
		meta.EndedAt = &endedAt
	}

	return meta, nil
}
