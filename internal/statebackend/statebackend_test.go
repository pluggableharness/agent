package statebackend

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
)

// fixedClock returns a Store clock (WithClock) that always reports t.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// newTestStore returns a Store rooted at a fresh t.TempDir().
func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	st, err := NewStore(t.TempDir(), opts...)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return st
}

// createSession creates a session in st and registers its Close via
// t.Cleanup, failing the test immediately on error.
func createSession(t *testing.T, st *Store, meta SessionMeta) *Session {
	t.Helper()
	sess, err := st.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("Create %s: %v", meta.SessionID, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestNewStore(t *testing.T) {
	t.Parallel()

	t.Run("creates directory with 0700", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "sessions")
		if _, err := NewStore(dir); err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if !info.IsDir() {
			t.Fatal("dir is not a directory")
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("dir perm = %o, want %o", perm, 0o700)
		}
	})

	t.Run("empty dir rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := NewStore(""); err == nil {
			t.Fatal("NewStore(\"\") = nil error, want error")
		}
	})
}

func TestStore_Create(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	st := newTestStore(t, WithClock(fixedClock(fixedNow)))
	ctx := context.Background()

	id := NewSessionID(fixedNow)
	meta := SessionMeta{
		SessionID: id,
		Profile:   "default",
		Status:    sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		Depth:     2,
		// StartedAt deliberately left zero to exercise the Store-clock default.
	}

	sess := createSession(t, st, meta)
	if sess.ID() != id {
		t.Errorf("ID() = %q, want %q", sess.ID(), id)
	}

	path := st.sessionPath(id)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want %o", perm, 0o600)
	}

	version, err := readUserVersion(ctx, sess.db)
	if err != nil {
		t.Fatalf("readUserVersion: %v", err)
	}
	if version != currentSchemaVersion {
		t.Errorf("user_version = %d, want %d", version, currentSchemaVersion)
	}

	got, err := querySessionMeta(ctx, sess.db, id)
	if err != nil {
		t.Fatalf("querySessionMeta: %v", err)
	}
	if got.SessionID != id {
		t.Errorf("SessionID = %q, want %q", got.SessionID, id)
	}
	if got.ParentSessionID != "" {
		t.Errorf("ParentSessionID = %q, want empty", got.ParentSessionID)
	}
	if got.Profile != meta.Profile {
		t.Errorf("Profile = %q, want %q", got.Profile, meta.Profile)
	}
	if got.Status != meta.Status {
		t.Errorf("Status = %v, want %v", got.Status, meta.Status)
	}
	if got.Depth != meta.Depth {
		t.Errorf("Depth = %d, want %d", got.Depth, meta.Depth)
	}
	if !got.StartedAt.Equal(fixedNow) {
		t.Errorf("StartedAt = %v, want %v (Store clock default)", got.StartedAt, fixedNow)
	}
	if got.EndedAt != nil {
		t.Errorf("EndedAt = %v, want nil", got.EndedAt)
	}
}

func TestStore_Create_endedAtRoundTrips(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ended := started.Add(5 * time.Minute)
	id := NewSessionID(started)
	meta := SessionMeta{
		SessionID: id,
		Profile:   "default",
		Status:    sessionv1.SessionStatus_SESSION_STATUS_COMPLETED,
		StartedAt: started,
		EndedAt:   &ended,
	}

	sess := createSession(t, st, meta)
	got, err := querySessionMeta(ctx, sess.db, id)
	if err != nil {
		t.Fatalf("querySessionMeta: %v", err)
	}
	if got.EndedAt == nil {
		t.Fatal("EndedAt = nil, want non-nil")
	}
	if !got.EndedAt.Equal(ended) {
		t.Errorf("EndedAt = %v, want %v", *got.EndedAt, ended)
	}
}

func TestStore_Create_duplicate(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	id := NewSessionID(time.Now())
	meta := SessionMeta{SessionID: id, Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: time.Now()}

	createSession(t, st, meta)

	if _, err := st.Create(context.Background(), meta); err == nil {
		t.Fatal("Create (duplicate session id) = nil error, want error")
	}
}

func TestStore_Create_invalidSessionID(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := st.Create(context.Background(), SessionMeta{SessionID: "not-a-ulid"}); err == nil {
		t.Fatal("Create with invalid session id = nil error, want error")
	}
}

func TestStore_Create_invalidStatus(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	id := NewSessionID(time.Now())
	meta := SessionMeta{SessionID: id, Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_UNSPECIFIED, StartedAt: time.Now()}
	if _, err := st.Create(context.Background(), meta); err == nil {
		t.Fatal("Create with SESSION_STATUS_UNSPECIFIED = nil error, want error")
	}

	// The partially-created file must not survive a failed Create — it
	// would otherwise permanently block a retry with the same session ID
	// (Create uses O_EXCL and treats an existing file as a real conflict).
	if _, err := os.Stat(st.sessionPath(id)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("session file after failed Create: err = %v, want fs.ErrNotExist", err)
	}
}

func TestStore_Open(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	id := NewSessionID(now)
	meta := SessionMeta{SessionID: id, Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: now}

	created, err := st.Create(ctx, meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := created.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sess, err := st.Open(ctx, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	if sess.ID() != id {
		t.Errorf("ID() = %q, want %q", sess.ID(), id)
	}

	got, err := querySessionMeta(ctx, sess.db, id)
	if err != nil {
		t.Fatalf("querySessionMeta: %v", err)
	}
	if got.Profile != meta.Profile {
		t.Errorf("Profile = %q, want %q", got.Profile, meta.Profile)
	}
}

func TestStore_Open_notFound(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	_, err := st.Open(context.Background(), NewSessionID(time.Now()))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Open (missing session) err = %v, want ErrNotFound", err)
	}
}

func TestStore_Open_invalidSessionID(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := st.Open(context.Background(), "not-a-ulid"); err == nil {
		t.Fatal("Open with invalid session id = nil error, want error")
	}
}

func TestStore_Open_schemaTooNew(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	id := NewSessionID(time.Now())
	path := st.sessionPath(id)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), "PRAGMA user_version = 999"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = st.Open(context.Background(), id)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("Open (newer schema) err = %v, want ErrSchemaTooNew", err)
	}
}

func TestStore_Open_olderSchemaWithNoMigrationPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	id := NewSessionID(time.Now())
	path := st.sessionPath(id)

	// A bare file at user_version 0 (the pre-schema default): older than
	// currentSchemaVersion, so Open must take the migration branch — which,
	// since the real migrations slice is empty for schema version 1, has no
	// path from 0 to 1 and must fail rather than silently proceed.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// sql.Open is lazy — force the file into existence before Close.
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := st.Open(context.Background(), id); err == nil {
		t.Fatal("Open (schema older than current, no migration path) = nil error, want error")
	}
}

func TestStore_ListAndChildren(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	root := NewSessionID(base)
	createSession(t, st, SessionMeta{SessionID: root, Profile: "root", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: base})

	child1 := NewSessionID(base.Add(time.Millisecond))
	createSession(t, st, SessionMeta{SessionID: child1, ParentSessionID: root, Profile: "child", Status: sessionv1.SessionStatus_SESSION_STATUS_COMPLETED, StartedAt: base.Add(time.Millisecond)})

	child2 := NewSessionID(base.Add(2 * time.Millisecond))
	createSession(t, st, SessionMeta{SessionID: child2, ParentSessionID: root, Profile: "child", Status: sessionv1.SessionStatus_SESSION_STATUS_FAILED, StartedAt: base.Add(2 * time.Millisecond)})

	other := NewSessionID(base.Add(3 * time.Millisecond))
	createSession(t, st, SessionMeta{SessionID: other, Profile: "unrelated", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: base.Add(3 * time.Millisecond)})

	all, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List returned %d sessions, want 4", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].SessionID >= all[i].SessionID {
			t.Errorf("List not ordered by session_id: %q before %q", all[i-1].SessionID, all[i].SessionID)
		}
	}

	children, err := st.Children(ctx, root)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("Children returned %d sessions, want 2", len(children))
	}
	if children[0].SessionID != child1 || children[1].SessionID != child2 {
		t.Errorf("Children = [%q, %q], want [%q, %q]", children[0].SessionID, children[1].SessionID, child1, child2)
	}
	for _, c := range children {
		if c.ParentSessionID != root {
			t.Errorf("child %q ParentSessionID = %q, want %q", c.SessionID, c.ParentSessionID, root)
		}
	}

	noChildren, err := st.Children(ctx, other)
	if err != nil {
		t.Fatalf("Children(other): %v", err)
	}
	if len(noChildren) != 0 {
		t.Errorf("Children(other) = %d sessions, want 0", len(noChildren))
	}
}

func TestStore_List_empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	all, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List = %d sessions, want 0", len(all))
	}
}

func TestStore_Children_invalidSessionID(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := st.Children(context.Background(), "not-a-ulid"); err == nil {
		t.Fatal("Children with invalid session id = nil error, want error")
	}
}

func TestStore_scan_skipsNonSessionFiles(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx := context.Background()

	id := NewSessionID(time.Now())
	createSession(t, st, SessionMeta{SessionID: id, Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: time.Now()})

	// A file that isn't a valid session ID's .sqlite (e.g. a future
	// Stage 3 *.sqlite.corrupt sidecar) must be skipped, not fail the scan.
	if err := os.WriteFile(filepath.Join(st.dir, "not-a-session.sqlite"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(st.dir, "README.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	all, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].SessionID != id {
		t.Errorf("List = %v, want exactly [%q]", all, id)
	}
}

func TestSessionStatus_roundTrip(t *testing.T) {
	t.Parallel()

	for status := range sessionStatusText {
		text, err := encodeSessionStatus(status)
		if err != nil {
			t.Fatalf("encodeSessionStatus(%v): %v", status, err)
		}
		got, err := decodeSessionStatus(text)
		if err != nil {
			t.Fatalf("decodeSessionStatus(%q): %v", text, err)
		}
		if got != status {
			t.Errorf("round trip %v -> %q -> %v, want %v", status, text, got, status)
		}
	}
}

func TestEncodeSessionStatus_unspecifiedRejected(t *testing.T) {
	t.Parallel()
	if _, err := encodeSessionStatus(sessionv1.SessionStatus_SESSION_STATUS_UNSPECIFIED); err == nil {
		t.Fatal("encodeSessionStatus(UNSPECIFIED) = nil error, want error")
	}
}

func TestDecodeSessionStatus_unrecognized(t *testing.T) {
	t.Parallel()
	if _, err := decodeSessionStatus("not_a_status"); err == nil {
		t.Fatal("decodeSessionStatus(garbage) = nil error, want error")
	}
}

func TestNewStore_mkdirFails(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// blocker is a regular file, not a directory, so MkdirAll(blocker/sessions)
	// must fail with ENOTDIR.
	if _, err := NewStore(filepath.Join(blocker, "sessions")); err == nil {
		t.Fatal("NewStore under a non-directory parent = nil error, want error")
	}
}

func TestStore_Create_openFileError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if err := os.Chmod(st.dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	defer func() { _ = os.Chmod(st.dir, 0o700) }()

	meta := SessionMeta{SessionID: NewSessionID(time.Now()), Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: time.Now()}
	if _, err := st.Create(context.Background(), meta); err == nil {
		t.Fatal("Create in a read-only directory = nil error, want error")
	}
}

func TestOpenDB_directoryPathFails(t *testing.T) {
	t.Parallel()
	if _, err := openDB(context.Background(), t.TempDir()); err == nil {
		t.Fatal("openDB(directory) = nil error, want error")
	}
}

// TestOpenDB_foreignKeysEnabled locks in the security-review fix: since
// foreign_keys is a per-connection setting, it's requested via openDB's
// DSN (_pragma=foreign_keys(1)) rather than a one-time PRAGMA exec, so a
// replacement pooled connection still gets it. This asserts the
// observable effect — PRAGMA foreign_keys reads back 1 — rather than
// inspecting the DSN string itself.
func TestOpenDB_foreignKeysEnabled(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := openDB(context.Background(), path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var enabled int
	if err := db.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if enabled != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", enabled)
	}
}

func TestPopulateCreatedFile_openDBFailure(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	dirAsFile := filepath.Join(st.dir, "not-a-file")
	if err := os.Mkdir(dirAsFile, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	meta := SessionMeta{SessionID: NewSessionID(time.Now()), Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: time.Now()}
	if _, err := st.populateCreatedFile(context.Background(), dirAsFile, meta); err == nil {
		t.Fatal("populateCreatedFile against a directory = nil error, want error")
	}
}

func TestPopulateCreatedFile_initSchemaFailure(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	path := filepath.Join(st.dir, "existing.sqlite")

	db, err := openDB(context.Background(), path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	if err := initSchema(context.Background(), db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// path already carries the full schema; populateCreatedFile's own
	// initSchema call must fail (tables already exist) rather than silently
	// succeed against a file it didn't create.
	meta := SessionMeta{SessionID: NewSessionID(time.Now()), Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: time.Now()}
	if _, err := st.populateCreatedFile(context.Background(), path, meta); err == nil {
		t.Fatal("populateCreatedFile against an already-initialized file = nil error, want error")
	}
}

func TestStore_List_readDirError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if _, err := st.List(context.Background()); err == nil {
		t.Fatal("List after the store directory was removed = nil error, want error")
	}
}

func TestStore_scan_corruptFileReturnsError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	id := NewSessionID(time.Now())
	path := st.sessionPath(id)

	// A validly-named session file (per ValidateSessionID) whose content
	// isn't a sqlite database at all — distinct from
	// TestStore_scan_skipsNonSessionFiles, where the *filename* itself
	// isn't a valid session ID and is skipped outright. This one must
	// surface as an error, not be silently skipped.
	if err := os.WriteFile(path, []byte("this is not a valid sqlite file"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := st.List(context.Background()); err == nil {
		t.Fatal("List with a corrupt session file = nil error, want error")
	}
}

func TestInsertSessionMeta_duplicateSessionIDFails(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	meta := SessionMeta{SessionID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: time.Now()}
	if err := insertSessionMeta(ctx, db, meta); err != nil {
		t.Fatalf("insertSessionMeta: %v", err)
	}
	if err := insertSessionMeta(ctx, db, meta); err == nil {
		t.Fatal("insertSessionMeta (duplicate session_id) = nil error, want error")
	}
}

func TestQuerySessionMeta_invalidStatus(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	const q = `INSERT INTO session_meta (session_id, parent_session_id, profile, status, depth, started_at, ended_at) VALUES (?, NULL, ?, ?, ?, ?, NULL)`
	if _, err := db.ExecContext(ctx, q, "01ARZ3NDEKTSV4RRFFQ69G5FAV", "default", "not_a_status", 0, formatTimestamp(time.Now())); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := querySessionMeta(ctx, db, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); err == nil {
		t.Fatal("querySessionMeta with invalid status = nil error, want error")
	}
}

func TestQuerySessionMeta_invalidStartedAt(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	const q = `INSERT INTO session_meta (session_id, parent_session_id, profile, status, depth, started_at, ended_at) VALUES (?, NULL, ?, ?, ?, ?, NULL)`
	if _, err := db.ExecContext(ctx, q, "01ARZ3NDEKTSV4RRFFQ69G5FAV", "default", "running", 0, "not-a-timestamp"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := querySessionMeta(ctx, db, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); err == nil {
		t.Fatal("querySessionMeta with invalid started_at = nil error, want error")
	}
}

func TestQuerySessionMeta_invalidEndedAt(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	const q = `INSERT INTO session_meta (session_id, parent_session_id, profile, status, depth, started_at, ended_at) VALUES (?, NULL, ?, ?, ?, ?, ?)`
	if _, err := db.ExecContext(ctx, q, "01ARZ3NDEKTSV4RRFFQ69G5FAV", "default", "running", 0, formatTimestamp(time.Now()), "not-a-timestamp"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := querySessionMeta(ctx, db, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); err == nil {
		t.Fatal("querySessionMeta with invalid ended_at = nil error, want error")
	}
}

func TestWithLogger(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st := newTestStore(t, WithLogger(logger))

	createSession(t, st, SessionMeta{SessionID: NewSessionID(time.Now()), Profile: "default", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING, StartedAt: time.Now()})

	if !strings.Contains(buf.String(), "creating session") {
		t.Errorf("logger output = %q, want it to contain %q", buf.String(), "creating session")
	}
}

func TestWithLogger_nilIgnored(t *testing.T) {
	t.Parallel()
	st := newTestStore(t, WithLogger(nil))
	if st.logger == nil {
		t.Error("logger = nil, want the default of slog.Default()")
	}
}

func TestTimestamp_roundTrip(t *testing.T) {
	t.Parallel()

	in := time.Date(2026, 3, 4, 5, 6, 7, 123_000_000, time.FixedZone("EST", -5*60*60))
	text := formatTimestamp(in)

	got, err := parseTimestamp(text)
	if err != nil {
		t.Fatalf("parseTimestamp(%q): %v", text, err)
	}
	if !got.Equal(in) {
		t.Errorf("round trip = %v, want %v", got, in)
	}
	if got.Location() != time.UTC {
		t.Errorf("parsed location = %v, want UTC", got.Location())
	}
}
