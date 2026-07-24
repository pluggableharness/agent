package statebackend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestStoreWithLogBuffer returns a Store (like newTestStore) whose
// WithLogger writes DEBUG-and-up records to the returned buffer, for tests
// asserting that recovery logs a WARN — matching the bytes.Buffer +
// slog.TextHandler recording-handler convention used throughout this
// package (e.g. statebackend_test.go's TestWithLogger).
func newTestStoreWithLogBuffer(t *testing.T, opts ...Option) (*Store, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st := newTestStore(t, append([]Option{WithLogger(logger)}, opts...)...)
	return st, &buf
}

// corruptRegion overwrites length bytes at offset with a repeating,
// clearly-not-original pattern, simulating localized on-disk corruption
// (e.g. a bad sector, a partial write) without touching the file's
// header — the file must remain openable, but PRAGMA integrity_check must
// find something wrong with its data pages.
func corruptRegion(t *testing.T, path string, offset, length int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600) // #nosec G304 -- test helper operating on its own t.TempDir()-rooted fixture path
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() { _ = f.Close() }()

	garbage := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, length/4+1)[:length]
	if _, err := f.WriteAt(garbage, int64(offset)); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
}

// corruptHeader overwrites the file's 16-byte "SQLite format 3\000" magic
// header, which sqlite validates on every connection — this reliably
// makes even a basic PRAGMA fail with "file is not a database," simulating
// a file too destroyed to open at all, as opposed to corruptRegion's
// page-level damage that PRAGMA integrity_check specifically detects.
func corruptHeader(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600) // #nosec G304 -- test helper operating on its own t.TempDir()-rooted fixture path
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xFF}, 16), 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
}

func TestStore_Open_healthyFileNoRecovery(t *testing.T) {
	t.Parallel()
	st, logBuf := newTestStoreWithLogBuffer(t)
	meta := testSessionMeta()
	created := createSession(t, st, meta)

	for i := range 3 {
		if _, err := created.AppendEvent(context.Background(), testEvent(fmt.Sprintf("evt-%d", i))); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	opened, err := st.Open(context.Background(), meta.SessionID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	if strings.Contains(logBuf.String(), "recover") {
		t.Errorf("unexpected recovery log for a healthy file: %s", logBuf.String())
	}
	if _, statErr := os.Stat(st.sessionPath(meta.SessionID) + ".corrupt"); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf(".corrupt file exists for a healthy file: err=%v", statErr)
	}
}

// TestStore_Open_corruptionTriggersRecovery covers the recoverable-damage
// path: localized page corruption is caught by PRAGMA integrity_check, the
// damaged original is renamed to .corrupt, a fresh usable file with
// salvaged rows takes its place, and recovery is logged at WARN.
func TestStore_Open_corruptionTriggersRecovery(t *testing.T) {
	t.Parallel()
	st, logBuf := newTestStoreWithLogBuffer(t)
	meta := testSessionMeta()
	sess, err := st.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Enough data across several pages that corrupting a couple of
	// scattered regions is very likely to hit real, populated structure
	// rather than free space.
	const eventCount = 8
	for i := range eventCount {
		ev := testEvent(fmt.Sprintf("evt-%d", i))
		ev.Payload = bytes.Repeat([]byte{byte(i)}, 2048)
		if _, err := sess.AppendEvent(context.Background(), ev); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := st.sessionPath(meta.SessionID)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() < 32768 {
		t.Fatalf("file too small (%d bytes) for a reliable corruption test", info.Size())
	}
	// Table root pages are allocated at CREATE TABLE time (schema.go's
	// initSchema runs events, then session_meta, then the rest, all
	// before any row exists), so session_meta's root page — and its one
	// small row — lands early in the file, well before the additional
	// data pages events' later, larger-payload rows spill into as the
	// file grows. Trashing the back half of the file corrupts real,
	// populated event data pages with overwhelming likelihood while
	// leaving session_meta's early page untouched, so recovery has
	// something to actually salvage.
	corruptStart := int(info.Size()) / 2
	corruptRegion(t, path, corruptStart, int(info.Size())-corruptStart)

	opened, err := st.Open(context.Background(), meta.SessionID)
	if err != nil {
		t.Fatalf("Open (after corruption): %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	if _, statErr := os.Stat(path + ".corrupt"); statErr != nil {
		t.Errorf(".corrupt file missing after recovery: %v", statErr)
	}

	// The recovered file installed at the canonical path must carry the
	// same 0600 guarantee every session file does — recoverSession
	// pre-creates it itself rather than leaving the mode up to
	// sql.Open/modernc's own file creation (whose default is 0644 modulo
	// umask). Unix permission bits aren't meaningful on Windows, so this
	// assertion only runs on Unix-like systems.
	if runtime.GOOS != "windows" {
		recoveredInfo, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("Stat (recovered file): %v", statErr)
		}
		if perm := recoveredInfo.Mode().Perm(); perm != 0o600 {
			t.Errorf("recovered file perm = %o, want %o", perm, 0o600)
		}
	}

	gotMeta, err := opened.Meta(context.Background())
	if err != nil {
		t.Fatalf("Meta (recovered file): %v", err)
	}
	if gotMeta.SessionID != meta.SessionID {
		t.Errorf("recovered SessionID = %q, want %q", gotMeta.SessionID, meta.SessionID)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "recover") {
		t.Errorf("no recovery log captured; log = %q", logOutput)
	}
	if !strings.Contains(logOutput, meta.SessionID) {
		t.Errorf("recovery log missing session_id; log = %q", logOutput)
	}
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("recovery log not at WARN level; log = %q", logOutput)
	}
}

// TestStore_Open_fullyDestroyedFileUnrecoverable covers the unsalvageable
// case: the file's own header is destroyed, so neither the initial open
// nor a recovery attempt's own read of the (renamed) damaged file can
// succeed. Open must return ErrUnrecoverable, and the damaged file must
// still be renamed to .corrupt (never deleted, never left at its
// original name).
func TestStore_Open_fullyDestroyedFileUnrecoverable(t *testing.T) {
	t.Parallel()
	st, logBuf := newTestStoreWithLogBuffer(t)
	meta := testSessionMeta()
	sess, err := st.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := st.sessionPath(meta.SessionID)
	corruptHeader(t, path)

	_, err = st.Open(context.Background(), meta.SessionID)
	if !errors.Is(err, ErrUnrecoverable) {
		t.Fatalf("Open (destroyed file) err = %v, want ErrUnrecoverable", err)
	}

	if _, statErr := os.Stat(path + ".corrupt"); statErr != nil {
		t.Errorf(".corrupt file missing after unrecoverable failure: %v", statErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("original path still exists after being renamed to .corrupt: err=%v", statErr)
	}
	if _, statErr := os.Stat(path + ".recovering"); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("stale .recovering file left behind: err=%v", statErr)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "unreadable") {
		t.Errorf("no unreadable/failure log captured; log = %q", logOutput)
	}
}

func TestRunIntegrityCheck_healthy(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	problems, err := runIntegrityCheck(ctx, db)
	if err != nil {
		t.Fatalf("runIntegrityCheck: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}
}

func TestRecoverTable_unreadableTableReturnsZeros(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := openTestDB(t) // no schema applied: every table is "unreadable"
	dst := openTestDB(t)
	if err := initSchema(ctx, dst); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	salvaged, skipped := recoverTable(ctx, src, dst, recoveryTableSpecs[0])
	if salvaged != 0 || skipped != 0 {
		t.Errorf("recoverTable against an unreadable source = (%d, %d), want (0, 0)", salvaged, skipped)
	}
}

func TestRecoverSessionMeta_unreadableReturnsFalse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := openTestDB(t) // no schema: session_meta doesn't exist
	dst := openTestDB(t)
	if err := initSchema(ctx, dst); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	if recoverSessionMeta(ctx, src, dst, "any-id") {
		t.Error("recoverSessionMeta against an unreadable source = true, want false")
	}
}

func TestStore_recoverSession_installFailureLeavesDataAtRecoveringPath(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	meta := testSessionMeta()
	sess, err := st.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := st.sessionPath(meta.SessionID)
	corruptPath := path + ".corrupt"
	if err := os.Rename(path, corruptPath); err != nil {
		t.Fatalf("rename to .corrupt: %v", err)
	}

	// Occupy the install target with a directory so the final
	// os.Rename(recoveryPath, dstPath) inside recoverSession fails after
	// the recovery file is already fully built — recoverSession must not
	// delete the completed .recovering file in that case.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir (blocking the install path): %v", err)
	}

	_, _, err = st.recoverSession(context.Background(), corruptPath, path, meta.SessionID)
	if err == nil {
		t.Fatal("recoverSession (blocked install path) = nil error, want error")
	}

	if _, statErr := os.Stat(path + ".recovering"); statErr != nil {
		t.Errorf("recovered data at .recovering was not preserved after a failed install: %v", statErr)
	}
}

func TestStore_Open_notFoundStillReturnsErrNotFound(t *testing.T) {
	// Sanity check that the integrity-check rewrite of Open didn't fold
	// the plain "file doesn't exist" case into the corruption-recovery
	// path — a missing file is not the same thing as a damaged one.
	t.Parallel()
	st := newTestStore(t)
	_, err := st.Open(context.Background(), NewSessionID(time.Now()))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Open (missing file) err = %v, want ErrNotFound", err)
	}
}
