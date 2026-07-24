package statebackend

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// openTestDB opens a fresh, empty sqlite file under t.TempDir() directly
// (bypassing openDB/Store entirely), for tests exercising schema.go's
// functions in isolation.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return db
}

// tableExists reports whether name is a table in db's sqlite_master.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRowContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&got)
	switch {
	case err == nil:
		return true
	case errors.Is(err, sql.ErrNoRows):
		return false
	default:
		t.Fatalf("query sqlite_master for table %q: %v", name, err)
		return false
	}
}

// indexExists reports whether name is an index in db's sqlite_master.
func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRowContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?", name).Scan(&got)
	switch {
	case err == nil:
		return true
	case errors.Is(err, sql.ErrNoRows):
		return false
	default:
		t.Fatalf("query sqlite_master for index %q: %v", name, err)
		return false
	}
}

func TestInitSchema(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	for _, table := range []string{"events", "session_meta", "cost_ledger", "plan_items", "producers"} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q missing after initSchema", table)
		}
	}
	for _, idx := range []string{"idx_events_kind", "idx_events_producer"} {
		if !indexExists(t, db, idx) {
			t.Errorf("index %q missing after initSchema", idx)
		}
	}

	version, err := readUserVersion(ctx, db)
	if err != nil {
		t.Fatalf("readUserVersion: %v", err)
	}
	if version != currentSchemaVersion {
		t.Errorf("user_version = %d, want %d", version, currentSchemaVersion)
	}
}

func TestInitSchema_eventsAutoincrement(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema: %v", err)
	}

	// sqlite_sequence only exists when at least one table declares
	// INTEGER PRIMARY KEY AUTOINCREMENT — its presence confirms the DDL
	// kept AUTOINCREMENT verbatim rather than "optimizing" it away.
	if !tableExists(t, db, "sqlite_sequence") {
		t.Error("sqlite_sequence table missing — AUTOINCREMENT was not applied")
	}
}

func TestInitSchema_closedDBFailsToBegin(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := initSchema(context.Background(), db); err == nil {
		t.Fatal("initSchema on a closed db = nil error, want error")
	}
}

func TestInitSchema_reapplyFailsAndRollsBack(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	if err := initSchema(ctx, db); err != nil {
		t.Fatalf("initSchema (first): %v", err)
	}
	versionAfterFirst, err := readUserVersion(ctx, db)
	if err != nil {
		t.Fatalf("readUserVersion: %v", err)
	}

	// A second initSchema against the same, already-initialized db fails on
	// its first CREATE TABLE (events already exists) and must roll back
	// rather than leaving user_version half-applied.
	if err := initSchema(ctx, db); err == nil {
		t.Fatal("initSchema (second, against an already-initialized db) = nil error, want error")
	}

	versionAfterSecond, err := readUserVersion(ctx, db)
	if err != nil {
		t.Fatalf("readUserVersion: %v", err)
	}
	if versionAfterSecond != versionAfterFirst {
		t.Errorf("user_version = %d after failed reapply, want unchanged %d", versionAfterSecond, versionAfterFirst)
	}
}

func TestReadUserVersion_closedDB(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := readUserVersion(context.Background(), db); err == nil {
		t.Fatal("readUserVersion on a closed db = nil error, want error")
	}
}

func TestApplyMigrationStep_closedDBFailsToBegin(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	step := migrationStep{version: 1, migrate: func(context.Context, *sql.Tx) error { return nil }}
	if err := applyMigrationStep(context.Background(), db, step); err == nil {
		t.Fatal("applyMigrationStep on a closed db = nil error, want error")
	}
}

func TestReadUserVersion_defaultsZero(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	version, err := readUserVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("readUserVersion: %v", err)
	}
	if version != 0 {
		t.Errorf("version = %d, want 0", version)
	}
}

func TestApplyMigrations(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	var applied []int
	steps := []migrationStep{
		{version: 1, migrate: func(ctx context.Context, tx *sql.Tx) error {
			applied = append(applied, 1)
			_, err := tx.ExecContext(ctx, "CREATE TABLE migration_marker_1 (id INTEGER)")
			return err
		}},
		{version: 2, migrate: func(ctx context.Context, tx *sql.Tx) error {
			applied = append(applied, 2)
			_, err := tx.ExecContext(ctx, "CREATE TABLE migration_marker_2 (id INTEGER)")
			return err
		}},
	}

	if err := applyMigrations(ctx, db, steps, 0, 2); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}

	if len(applied) != 2 || applied[0] != 1 || applied[1] != 2 {
		t.Errorf("applied steps = %v, want [1 2]", applied)
	}

	version, err := readUserVersion(ctx, db)
	if err != nil {
		t.Fatalf("readUserVersion: %v", err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2", version)
	}

	for _, table := range []string{"migration_marker_1", "migration_marker_2"} {
		if !tableExists(t, db, table) {
			t.Errorf("table %q missing after migration", table)
		}
	}
}

func TestApplyMigrations_skipsAlreadyApplied(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	var applied []int
	steps := []migrationStep{
		{version: 1, migrate: func(context.Context, *sql.Tx) error {
			applied = append(applied, 1)
			return nil
		}},
		{version: 2, migrate: func(context.Context, *sql.Tx) error {
			applied = append(applied, 2)
			return nil
		}},
	}

	// from=1: step 1 is already applied and must be skipped, only step 2 runs.
	if err := applyMigrations(ctx, db, steps, 1, 2); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	if len(applied) != 1 || applied[0] != 2 {
		t.Errorf("applied steps = %v, want [2] (step 1 already applied)", applied)
	}
}

func TestApplyMigrations_alreadyAtTarget(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	stepRan := false
	steps := []migrationStep{
		{version: 1, migrate: func(context.Context, *sql.Tx) error {
			stepRan = true
			return nil
		}},
	}

	if err := applyMigrations(ctx, db, steps, 1, 1); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	if stepRan {
		t.Error("migration step ran when db was already at target version")
	}
}

func TestApplyMigrations_noPathToTarget(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	// No steps registered at all: target 1 is unreachable from 0.
	if err := applyMigrations(ctx, db, nil, 0, 1); err == nil {
		t.Fatal("applyMigrations = nil error, want error (no migration path)")
	}
}

// TestApplyMigrations_failingStepLeavesVersionUnchanged verifies the
// per-step transactionality applyMigrationStep documents: a step that
// fails midway rolls back both its own schema changes and the
// PRAGMA user_version stamp together, leaving the file exactly as it was
// before the attempt.
func TestApplyMigrations_failingStepLeavesVersionUnchanged(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	boom := errors.New("boom")
	steps := []migrationStep{
		{version: 1, migrate: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "CREATE TABLE should_not_persist (id INTEGER)"); err != nil {
				return err
			}
			return boom
		}},
	}

	err := applyMigrations(ctx, db, steps, 0, 1)
	if !errors.Is(err, boom) {
		t.Fatalf("applyMigrations err = %v, want wrapping %v", err, boom)
	}

	version, verr := readUserVersion(ctx, db)
	if verr != nil {
		t.Fatalf("readUserVersion: %v", verr)
	}
	if version != 0 {
		t.Errorf("user_version = %d, want 0 (unchanged after failed migration)", version)
	}

	if tableExists(t, db, "should_not_persist") {
		t.Error("should_not_persist table exists after a failed, rolled-back migration step")
	}
}

func TestApplyMigrations_multiStepPartialFailureStopsAtLastGood(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()

	boom := errors.New("boom")
	steps := []migrationStep{
		{version: 1, migrate: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "CREATE TABLE step_one (id INTEGER)")
			return err
		}},
		{version: 2, migrate: func(context.Context, *sql.Tx) error {
			return boom
		}},
	}

	err := applyMigrations(ctx, db, steps, 0, 2)
	if !errors.Is(err, boom) {
		t.Fatalf("applyMigrations err = %v, want wrapping %v", err, boom)
	}

	version, verr := readUserVersion(ctx, db)
	if verr != nil {
		t.Fatalf("readUserVersion: %v", verr)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1 (step 1 committed, step 2 rolled back)", version)
	}
	if !tableExists(t, db, "step_one") {
		t.Error("step_one table missing — step 1's commit should have survived step 2's failure")
	}
}
