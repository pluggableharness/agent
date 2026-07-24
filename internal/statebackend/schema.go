package statebackend

import (
	"context"
	"database/sql"
	"fmt"
)

// currentSchemaVersion is the schema revision this package implements
// (docs/specifications/state-backend.md#schema-migration). It is stamped
// into PRAGMA user_version at creation (initSchema) and checked — and
// migrated toward, if older — on every Open.
const currentSchemaVersion = 1

// schemaStatements is the five-table schema from
// docs/specifications/state-backend.md#schema, reproduced verbatim
// (including AUTOINCREMENT on every sequence column) as one DDL statement
// per slice element, in the document's own order, so each can be applied
// via database/sql's ExecContext — which does not guarantee multi-statement
// execution in a single call — rather than as one multi-statement string.
var schemaStatements = []string{
	`CREATE TABLE events (
  sequence          INTEGER PRIMARY KEY AUTOINCREMENT,
  id                TEXT NOT NULL UNIQUE,
  timestamp         TEXT NOT NULL,
  kind              TEXT NOT NULL,
  producer_category TEXT NOT NULL,
  producer_name     TEXT NOT NULL,
  producer_version  TEXT NOT NULL,
  schema_version    TEXT NOT NULL,
  payload           BLOB NOT NULL
)`,
	`CREATE INDEX idx_events_kind ON events(kind)`,
	`CREATE INDEX idx_events_producer ON events(producer_category, producer_name, producer_version)`,
	`CREATE TABLE session_meta (
  session_id         TEXT PRIMARY KEY,
  parent_session_id  TEXT,
  profile            TEXT NOT NULL,
  status             TEXT NOT NULL,
  depth              INTEGER NOT NULL,
  started_at         TEXT NOT NULL,
  ended_at           TEXT
)`,
	`CREATE TABLE cost_ledger (
  sequence           INTEGER PRIMARY KEY AUTOINCREMENT,
  event_sequence     INTEGER NOT NULL REFERENCES events(sequence),
  provider_name      TEXT NOT NULL,
  model_id           TEXT NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cost_usd           REAL NOT NULL
)`,
	`CREATE TABLE plan_items (
  sequence         INTEGER PRIMARY KEY AUTOINCREMENT,
  event_sequence   INTEGER NOT NULL REFERENCES events(sequence),
  turn_id          TEXT NOT NULL,
  tool_call_id     TEXT NOT NULL,
  provider_name    TEXT NOT NULL,
  tool_name        TEXT NOT NULL,
  decision         TEXT NOT NULL,
  decided_by       TEXT NOT NULL
)`,
	`CREATE TABLE producers (
  category             TEXT NOT NULL,
  name                 TEXT NOT NULL,
  version              TEXT NOT NULL,
  first_seen_sequence  INTEGER NOT NULL REFERENCES events(sequence),
  PRIMARY KEY (category, name, version)
)`,
}

// migrationStep is one ordered schema migration, applied transactionally by
// applyMigrations when opening a session file whose PRAGMA user_version is
// older than the target version it's being brought up to.
type migrationStep struct {
	// version is the user_version this step's migrate function produces.
	version int
	// migrate performs the step's schema changes within tx. It MUST NOT
	// touch PRAGMA user_version itself — applyMigrationStep stamps that in
	// the same transaction once migrate returns successfully.
	migrate func(ctx context.Context, tx *sql.Tx) error
}

// migrations is the ordered list of schema migrations, oldest to newest,
// consulted by applyMigrations when Open finds a session file whose
// PRAGMA user_version is older than currentSchemaVersion
// (docs/specifications/state-backend.md#schema-migration). It MUST stay
// sorted ascending by version. Empty for schema version 1: there is no
// schema version older than the current baseline to migrate from yet. A
// future schema bump adds one migrationStep here — never a change to the
// baseline schemaStatements above.
var migrations = []migrationStep{}

// initSchema creates the five-table schema and stamps
// PRAGMA user_version = currentSchemaVersion, in a single transaction, for
// a freshly created session file. Per
// docs/specifications/state-backend.md#schema-migration, every session file
// MUST carry user_version set at creation.
func initSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("statebackend: init schema: begin: %w", err)
	}
	for _, stmt := range schemaStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("statebackend: init schema: %w", err)
		}
	}
	// PRAGMA user_version takes an integer literal, not a bind parameter;
	// currentSchemaVersion is a package constant, never caller input, so
	// this is not a SQL-injection surface.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", currentSchemaVersion)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("statebackend: init schema: set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("statebackend: init schema: commit: %w", err)
	}
	return nil
}

// readUserVersion reads PRAGMA user_version from db. Per
// docs/specifications/state-backend.md#schema-migration this MUST be
// checked before any other operation touches an opened file — mirroring
// internal/registry's lock-file-version pre-check
// (internal/registry/lockfile.go's lockFileVersion).
func readUserVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("statebackend: read schema version: %w", err)
	}
	return version, nil
}

// applyMigrations brings db from schema version from up to target by
// running each step in steps whose version is greater than from, in
// ascending order, each inside its own transaction that also stamps
// PRAGMA user_version to that step's version
// (docs/specifications/state-backend.md#schema-migration). steps MUST be
// sorted ascending by version. Because the user_version write happens
// inside the same transaction as the step's own work, a failing step
// leaves the file at its last successfully applied version rather than
// partially migrated.
func applyMigrations(ctx context.Context, db *sql.DB, steps []migrationStep, from, target int) error {
	version := from
	for _, step := range steps {
		if version >= target {
			break
		}
		if step.version <= version {
			continue
		}
		if err := applyMigrationStep(ctx, db, step); err != nil {
			return err
		}
		version = step.version
	}
	if version != target {
		return fmt.Errorf("statebackend: migrate: reached schema version %d, want %d: no migration path", version, target)
	}
	return nil
}

// applyMigrationStep runs one migrationStep inside its own transaction,
// stamping PRAGMA user_version to step.version only after migrate succeeds,
// then commits. A failure at any point rolls the transaction back, leaving
// db's user_version unchanged.
func applyMigrationStep(ctx context.Context, db *sql.DB, step migrationStep) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("statebackend: migrate to version %d: begin: %w", step.version, err)
	}
	if err := step.migrate(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("statebackend: migrate to version %d: %w", step.version, err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", step.version)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("statebackend: migrate to version %d: set user_version: %w", step.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("statebackend: migrate to version %d: commit: %w", step.version, err)
	}
	return nil
}
